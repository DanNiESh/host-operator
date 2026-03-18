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
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/gophercloud/gophercloud/v2/openstack/baremetal/v1/nodes"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	osacopenshiftiov1alpha1 "github.com/DanNiESh/host-operator/api/v1alpha1"
	"github.com/DanNiESh/host-operator/pkg/ironic"
)

const hostManagementClass = "openstack"

// HostReconciler reconciles Host CRs.
type HostReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	IronicClient *ironic.Client
}

// +kubebuilder:rbac:groups=osac.openshift.io,resources=hosts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=osac.openshift.io,resources=hosts/status,verbs=get;update;patch
func (r *HostReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	host := &osacopenshiftiov1alpha1.Host{}
	if err := r.Get(ctx, req.NamespacedName, host); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// If the host is being deleted, skip the host management workflows.
	if !host.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// Check if the host should be managed by the controller.
	node := r.checkHostManaged(ctx, host, log)
	if node == nil {
		return ctrl.Result{}, nil
	}

	var res ctrl.Result

	desiredProv := desiredProvisioningState(host)
	if desiredProv != osacopenshiftiov1alpha1.ProvisioningStateAvailable &&
		desiredProv != osacopenshiftiov1alpha1.ProvisioningStateActive {
		log.V(1).Info("Host skipped", "reason", "spec.provisioning.state not active or available", "state", desiredProv)
		return ctrl.Result{}, nil
	}

	// Align power with desired provisioning: available: off, active: on.
	node, res = r.reconcilePower(ctx, host, node, desiredProv, log)
	if !res.IsZero() {
		if err := r.syncHostStatus(ctx, host, node); err != nil {
			return ctrl.Result{}, err
		}
		return res, nil
	}

	node, err := r.IronicClient.GetNode(ctx, host.Status.ID)
	if err != nil {
		log.Error(err, "reconcile failed: refresh node before sync")
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	if err := r.syncHostStatus(ctx, host, node); err != nil {
		return ctrl.Result{}, err
	}
	// Requeue the host if the node is still provisioning or deploy_failed.
	return r.reconcileRequeue(host, node, log)
}

func (r *HostReconciler) checkHostManaged(ctx context.Context, host *osacopenshiftiov1alpha1.Host, log logr.Logger) *nodes.Node {
	if host.Status.ID == "" {
		log.Info("Host skipped", "reason", "status.id not set")
		return nil
	}

	if hostManagementClass != "" {
		if host.Status.HostManagementClass == "" {
			log.Info("Host skipped", "reason", "status.hostManagementClass not set", "required", hostManagementClass)
			return nil
		}
		if host.Status.HostManagementClass != hostManagementClass {
			log.Info("Host skipped", "reason", "hostManagementClass mismatch", "want", hostManagementClass, "got", host.Status.HostManagementClass)
			return nil
		}
	} else if host.Status.HostManagementClass == "" {
		log.Info("Host skipped", "reason", "status.hostManagementClass not set")
		return nil
	}

	if r.IronicClient == nil {
		log.Info("Host skipped", "reason", "Ironic client not configured")
		return nil
	}

	nodeID := host.Status.ID
	node, err := r.IronicClient.GetNode(ctx, nodeID)
	if err != nil {
		log.Error(err, "Host skipped", "reason", "failed to get Ironic node", "nodeID", nodeID)
		return nil
	}
	log.V(1).Info("Ironic node", "nodeID", nodeID, "power_state", node.PowerState, "provision_state", node.ProvisionState)
	return node
}

func (r *HostReconciler) syncHostStatus(ctx context.Context, host *osacopenshiftiov1alpha1.Host, node *nodes.Node) error {
	host.Status.Provisioning.State = node.ProvisionState
	if p := host.Spec.Provisioning; p != nil && p.State == osacopenshiftiov1alpha1.ProvisioningStateActive {
		host.Status.Provisioning.URL = p.URL
	}
	poweredOn := powerStateToBool(node.PowerState)
	host.Status.PoweredOn = &poweredOn
	return r.Status().Update(ctx, host)
}

func desiredProvisioningState(host *osacopenshiftiov1alpha1.Host) string {
	if host.Spec.Provisioning == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(host.Spec.Provisioning.State))
}

func (r *HostReconciler) reconcileRequeue(host *osacopenshiftiov1alpha1.Host, node *nodes.Node, log logr.Logger) (ctrl.Result, error) {
	if ironic.IsProvisioning(node) {
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}
	if ironic.IsDeployFailed(node) {
		log.Info("Ironic deploy failed", "nodeID", host.Status.ID, "provision_state", node.ProvisionState)
	}
	return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
}

// reconcilePower sets Ironic power off when desired provisioning is available, on when active.
func (r *HostReconciler) reconcilePower(ctx context.Context, host *osacopenshiftiov1alpha1.Host, node *nodes.Node, desiredProv string, log logr.Logger) (*nodes.Node, ctrl.Result) {
	if r.IronicClient == nil {
		return node, ctrl.Result{}
	}
	id := host.Status.ID
	ps := strings.ToLower(strings.TrimSpace(node.PowerState))
	switch desiredProv {
	case osacopenshiftiov1alpha1.ProvisioningStateAvailable:
		if ps == "power off" {
			return node, ctrl.Result{}
		}
		log.Info("reconcile power: off for deprovisioning", "nodeID", id, "power_state", node.PowerState)
		if err := r.IronicClient.SetPowerState(ctx, id, "off"); err != nil {
			log.Error(err, "reconcile power: SetPowerState off failed")
			return node, ctrl.Result{RequeueAfter: 15 * time.Second}
		}
	case osacopenshiftiov1alpha1.ProvisioningStateActive:
		if ps == "power on" {
			return node, ctrl.Result{}
		}
		log.Info("reconcile power: on for provisioning", "nodeID", id, "power_state", node.PowerState)
		if err := r.IronicClient.SetPowerState(ctx, id, "on"); err != nil {
			log.Error(err, "reconcile power: SetPowerState on failed")
			return node, ctrl.Result{RequeueAfter: 15 * time.Second}
		}
	default:
		return node, ctrl.Result{}
	}
	n, err := r.IronicClient.GetNode(ctx, id)
	if err != nil {
		log.Error(err, "reconcile power: refresh node after power change failed")
		return node, ctrl.Result{RequeueAfter: 15 * time.Second}
	}
	return n, ctrl.Result{}
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
