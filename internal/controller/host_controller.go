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
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	osacopenshiftiov1alpha1 "github.com/DanNiESh/host-operator/api/v1alpha1"
	"github.com/DanNiESh/host-operator/internal/inventory"
)

// HostReconciler reconciles a Host object
type HostReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	InventoryClient *inventory.Client
}

// +kubebuilder:rbac:groups=osac.openshift.io,resources=hosts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=osac.openshift.io,resources=hosts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=hosts/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
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

	if r.InventoryClient == nil {
		log.Info("No inventory client configured, skipping power management")
		return ctrl.Result{}, nil
	}
	// Some testing here to see if it can reach the inventory service
	if err := r.InventoryClient.CheckConnectivity(ctx); err != nil {
		log.Error(err, "Cannot connect to Ironic service")
		// Update status to reflect connection failure if we add a condition
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Get current node state from Ironic
	node, err := r.InventoryClient.GetNode(ctx, host.Spec.NodeID)
	if err != nil {
		log.Error(err, "Failed to get node from Ironic")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Update status from Ironic (power state and provisioning state)
	poweredOn := powerStateToBool(node.PowerState)
	host.Status.PoweredOn = &poweredOn
	if host.Status.Provisioning.ID == "" {
		host.Status.Provisioning.ID = node.UUID
	}
	host.Status.Provisioning.State = osacopenshiftiov1alpha1.ProvisioningState(node.ProvisionState)
	if err := r.Status().Update(ctx, host); err != nil {
		return ctrl.Result{}, err
	}

	// If desired power (spec.Online) differs from current, set power state
	desiredOn := host.Spec.Online
	if desiredOn != poweredOn {
		target := "off"
		if desiredOn {
			target = "on"
		}
		if err := r.InventoryClient.SetPowerState(ctx, host.Spec.NodeID, target); err != nil {
			log.Error(err, "Failed to set power state", "target", target)
			return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
		}
		// Requeue to refresh status after power transition
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// No change needed; requeue periodically to refresh status
	return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
}

// powerStateToBool maps Ironic power_state string to bool. "power on" -> true, else false.
func powerStateToBool(powerState string) bool {
	return powerState == "power on"
}

// SetupWithManager sets up the controller with the Manager.
func (r *HostReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&osacopenshiftiov1alpha1.Host{}).
		Named("host").
		Complete(r)
}
