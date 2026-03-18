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
	var err error

	desiredProv := desiredProvisioningState(host)
	if desiredProv != osacopenshiftiov1alpha1.ProvisioningStateAvailable &&
		desiredProv != osacopenshiftiov1alpha1.ProvisioningStateActive {
		log.V(1).Info("Host skipped", "reason", "spec.provisioning.state not active or available", "state", desiredProv)
		return ctrl.Result{}, nil
	}
	// If the desired provisioning state is available, reconcile the host to be available.
	if desiredProv == osacopenshiftiov1alpha1.ProvisioningStateAvailable {
		res = r.reconcileDesiredAvailable(ctx, node, host.Status.ID, log)
		node, err = r.IronicClient.GetNode(ctx, host.Status.ID)
		if err != nil {
			if !res.IsZero() {
				return res, nil
			}
			log.Error(err, "reconcile failed: refresh node after moving toward available")
			return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
		}
		if !res.IsZero() {
			if err := r.syncHostStatus(ctx, host, node); err != nil {
				return ctrl.Result{}, err
			}
			return res, nil
		}
	}
	// If the desired provisioning state is active, reconcile the host to be active.
	if desiredProv == osacopenshiftiov1alpha1.ProvisioningStateActive {
		res = r.reconcileDesiredActive(ctx, host, node, log)
		node, err = r.IronicClient.GetNode(ctx, host.Status.ID)
		if err != nil {
			if !res.IsZero() {
				return res, nil
			}
			log.Error(err, "reconcile failed: refresh node after moving toward active")
			return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
		}
		if !res.IsZero() {
			if err := r.syncHostStatus(ctx, host, node); err != nil {
				return ctrl.Result{}, err
			}
			return res, nil
		}
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

func (r *HostReconciler) reconcileDesiredAvailable(ctx context.Context, node *nodes.Node, nodeID string, log logr.Logger) ctrl.Result {
	ps := nodes.ProvisionState(node.ProvisionState)

	if ps == nodes.Available {
		log.Info("Node is available, skipping reconciliation")
		return ctrl.Result{}
	}

	if ps == nodes.Manageable {
		// Move the node from manageable to available state.
		if err := r.IronicClient.ChangeProvisionState(ctx, nodeID, nodes.ProvisionStateOpts{Target: nodes.TargetProvide}); err != nil {
			log.Error(err, "reconcile available failed", "reason", "TargetProvide failed")
			return ctrl.Result{RequeueAfter: 15 * time.Second}
		}
		return ctrl.Result{RequeueAfter: 15 * time.Second}
	}

	if ironic.CanDeprovision(node) {
		// Move the node from active to available state.
		if err := r.IronicClient.ChangeProvisionState(ctx, nodeID, nodes.ProvisionStateOpts{
			Target: nodes.TargetDeleted,
		}); err != nil {
			log.Error(err, "reconcile available failed", "reason", "TargetDeleted failed")
			return ctrl.Result{RequeueAfter: 15 * time.Second}
		}
		return ctrl.Result{RequeueAfter: 15 * time.Second}
	}

	if ironic.IsProvisioning(node) {
		log.V(1).Info("Waiting for node toward available", "provision_state", node.ProvisionState)
		return ctrl.Result{RequeueAfter: 15 * time.Second}
	}

	return ctrl.Result{}
}

func (r *HostReconciler) reconcileDesiredActive(ctx context.Context, host *osacopenshiftiov1alpha1.Host, node *nodes.Node, log logr.Logger) ctrl.Result {
	p := host.Spec.Provisioning
	isoURL := strings.TrimSpace(p.URL)
	if isoURL == "" || strings.TrimSpace(p.ProvisioningNetwork) == "" {
		return ctrl.Result{}
	}
	if ironic.IsProvisioning(node) {
		log.Info("Node is already deploying")
		return ctrl.Result{}
	}
	if ironic.IsProvisioned(node) {
		log.Info("Node is already deployed")
		return ctrl.Result{}
	}
	if err := r.IronicClient.ProvisionWithISO(ctx, host.Status.ID, isoURL); err != nil {
		switch {
		case errors.Is(err, ironic.ErrNodeNotAvailable):
			log.Info("Provision deferred", "reason", err.Error())
		case errors.Is(err, ironic.ErrValidationFailed):
			log.Error(err, "Ironic validation failed before deploy")
		default:
			log.Error(err, "ProvisionWithISO failed")
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}
	}
	return ctrl.Result{}
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
