/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2/openstack/baremetal/v1/nodes"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	osacopenshiftiov1alpha1 "github.com/DanNiESh/host-operator/api/v1alpha1"
	"github.com/DanNiESh/host-operator/pkg/ironic"
)

// HostReconciler reconciles a Host object
type HostReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	IronicClient *ironic.Client
}

// +kubebuilder:rbac:groups=osac.openshift.io,resources=hosts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=osac.openshift.io,resources=hosts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=hosts/finalizers,verbs=update

func (r *HostReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	host := &osacopenshiftiov1alpha1.Host{}
	if err := r.Get(ctx, req.NamespacedName, host); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if host.Spec.NodeID == "" {
		log.Info("Host has no nodeID, skipping reconcile")
		return ctrl.Result{}, nil
	}

	if r.IronicClient == nil {
		log.Info("No Ironic client configured")
		return ctrl.Result{}, nil
	}

	node, err := r.IronicClient.GetNode(ctx, host.Spec.NodeID)
	if err != nil {
		log.Error(err, "Failed to get node from Ironic")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
	log.V(1).Info("Got node from Ironic", "nodeID", host.Spec.NodeID, "power_state", node.PowerState, "provision_state", node.ProvisionState)

	isoURL := r.liveISOURL(host)
	wantProvision := isoURL != ""

	if wantProvision {
		if err := r.IronicClient.ProvisionWithISO(ctx, host.Spec.NodeID, isoURL); err != nil {
			switch {
			case errors.Is(err, ironic.ErrNodeNotAvailable):
				log.Info("Node not ready for provisioning yet", "reason", err.Error())
			case errors.Is(err, ironic.ErrValidationFailed):
				log.Error(err, "Ironic validation failed before deploy")
			default:
				log.Error(err, "ProvisionWithISO failed")
			}
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		node, err = r.IronicClient.GetNode(ctx, host.Spec.NodeID)
		if err != nil {
			log.Error(err, "Failed to refresh node after provision step")
			return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
		}
	}

	if !wantProvision && ironic.CanDeprovision(node) {
		if err := r.IronicClient.ChangeProvisionState(ctx, host.Spec.NodeID, nodes.ProvisionStateOpts{
			Target: nodes.TargetDeleted,
		}); err != nil {
			log.Error(err, "Failed to start deprovision")
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		node, err = r.IronicClient.GetNode(ctx, host.Spec.NodeID)
		if err != nil {
			return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
		}
	}

	if err := r.syncHostStatus(ctx, host, node); err != nil {
		return ctrl.Result{}, err
	}

	if ironic.IsProvisioning(node) {
		return ctrl.Result{RequeueAfter: ironic.DefaultPollInterval}, nil
	}
	if ironic.IsDeployFailed(node) {
		log.Info("Ironic reports deploy failed", "nodeID", host.Spec.NodeID, "provision_state", node.ProvisionState)
	}

	if !ironic.IsProvisioning(node) {
		desiredOn := host.Spec.Online
		poweredOn := powerStateToBool(node.PowerState)
		if desiredOn != poweredOn {
			target := "off"
			if desiredOn {
				target = "on"
			}
			if err := r.IronicClient.SetPowerState(ctx, host.Spec.NodeID, target); err != nil {
				log.Error(err, "Failed to set power state", "target", target)
				return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
			}
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
	}

	return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
}

func (r *HostReconciler) syncHostStatus(ctx context.Context, host *osacopenshiftiov1alpha1.Host, node *nodes.Node) error {
	poweredOn := powerStateToBool(node.PowerState)
	host.Status.PoweredOn = &poweredOn
	if host.Status.Provisioning.ID == "" {
		host.Status.Provisioning.ID = node.UUID
	}
	host.Status.Provisioning.State = osacopenshiftiov1alpha1.ProvisioningState(node.ProvisionState)
	if u := r.liveISOURL(host); u != "" {
		host.Status.Provisioning.Image = osacopenshiftiov1alpha1.Image{URL: u, Format: "live-iso"}
	}
	return r.Status().Update(ctx, host)
}

func (r *HostReconciler) liveISOURL(host *osacopenshiftiov1alpha1.Host) string {
	if host.Spec.Image == nil {
		return ""
	}
	u := strings.TrimSpace(host.Spec.Image.URL)
	if u == "" {
		return ""
	}
	return u
}

func powerStateToBool(powerState string) bool {
	return powerState == "power on"
}

func (r *HostReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&osacopenshiftiov1alpha1.Host{}).
		Named("host").
		Complete(r)
}
