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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// HostSpec defines the desired state of Host.
type HostSpec struct {
	// Matches is a struct of constraints for when selecting a host from the inventory.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="field is immutable"
	Matches MatchExpressions `json:"matches"`
	// SetUpWorkflow defines the workflow to run when provisioning the host (e.g. deploy image).
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="field is immutable"
	SetUpWorkflow *WorkflowSpec `json:"setUpWorkflow,omitempty"`
	// TearDownWorkflow defines the workflow to run when deprovisioning the host.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="field is immutable"
	TearDownWorkflow *WorkflowSpec `json:"tearDownWorkflow,omitempty"`
	// Online is the desired power state (true = on, false = off).
	Online bool `json:"online"`
	// NetworkInterfaces lists the host's network interfaces with desired network binding.
	NetworkInterfaces []NetworkInterfaceSpec `json:"networkInterfaces,omitempty"`
	// Provisioning holds the desired Ironic state and
	// when active, image-based URL and provisioning network (e.g. external).
	Provisioning *ProvisioningSpec `json:"provisioning,omitempty"`
}

// Provisioning state values for spec.provisioning.state.
const (
	ProvisioningStateActive    = "active"
	ProvisioningStateAvailable = "available"
)

type MatchExpressions struct {
	// ManagedBy identifies the controller or system that manages this host.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="field is immutable"
	ManagedBy string `json:"managedBy"`
	// HostClass is the class/capability tier of the host.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="field is immutable"
	HostClass string `json:"hostClass"`
	// Query is a map of additional values to filter through
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="field is immutable"
	Query map[string]string `json:"query,omitempty"`
	// ProvisionState filters for hosts in this provision state (e.g. available).
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="field is immutable"
	ProvisionState string `json:"provisionState,omitempty"`
}

// WorkflowSpec references a workflow and its input.
type WorkflowSpec struct {
	// WorkflowID identifies the workflow to run.
	WorkflowID string `json:"workflowID"`
	// Input maps workflow parameter keys to values (workflow-specific).
	Input map[string]string `json:"input,omitempty"`
}

// NetworkInterfaceSpec describes a desired network interface and its network binding.
type NetworkInterfaceSpec struct {
	// MACAddress is the interface MAC address.
	MACAddress string `json:"macAddress,omitempty"`
	// Network is the network to attach this interface to (e.g. private-vlan-network).
	Network string `json:"network,omitempty"`
}

// ProvisioningSpec holds desired provisioning parameters.
// +kubebuilder:validation:XValidation:rule="self.state != 'active' || (size(self.url) > 0 && size(self.provisioningNetwork) > 0)",message="when state is active, url and provisioningNetwork must be set"
type ProvisioningSpec struct {
	// State is the desired Ironic provisioning outcome: active (deployed) or available (in pool).
	// +kubebuilder:validation:Enum=active;available
	State string `json:"state,omitempty"`
	// URL is set when state is active for image-based provisioning.
	URL string `json:"url,omitempty"`
	// ProvisioningNetwork identifies the provisioning network model.
	ProvisioningNetwork string `json:"provisioningNetwork,omitempty"`
}

// HostStatus defines the observed state of Host.
type HostStatus struct {
	SetUpWorkflowOutput map[string]string `json:"setUpWorkflowOutput,omitempty"`
	// ID is the host ID from inventory (used by Host Management Operator as node identifier).
	ID string `json:"id,omitempty"`
	// Name is the host name from inventory.
	Name string `json:"name,omitempty"`
	// HostManagementClass is set by the Host Inventory Operator (e.g. openstack).
	HostManagementClass string `json:"hostManagementClass,omitempty"`
	// NetworkClass is the network class for this host (e.g. openstack).
	NetworkClass string `json:"networkClass,omitempty"`
	// PoweredOn is the current power state.
	PoweredOn *bool `json:"poweredOn,omitempty"`
	// NetworkInterfaces lists the host's network interfaces (from inventory or observed).
	NetworkInterfaces []NetworkInterfaceStatus `json:"networkInterfaces,omitempty"`
	// Provisioning holds current provisioning URL and state from the backend.
	Provisioning ProvisionStatus `json:"provisioning,omitempty"`
}

// NetworkInterfaceStatus describes an observed network interface.
type NetworkInterfaceStatus struct {
	// MACAddress is the interface MAC address.
	MACAddress string `json:"macAddress,omitempty"`
}

// ProvisionStatus holds current provisioning state from the backend.
type ProvisionStatus struct {
	// URL is the URL of the currently provisioned image.
	URL string `json:"url,omitempty"`
	// State is the current provisioning state (e.g. active).
	State string `json:"state,omitempty"`
}

// GetPoolID returns the owning BareMetalPool UID if the Host is owned by a BareMetalPool.
func (h *Host) GetPoolID() (string, bool) {
	for _, ownerReference := range h.OwnerReferences {
		if ownerReference.Controller == nil || !*ownerReference.Controller {
			continue
		}
		if ownerReference.APIVersion == h.APIVersion && ownerReference.Kind == "BareMetalPool" {
			return string(ownerReference.UID), true
		}
	}
	return "", false
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Host is the Schema for the hosts API.
type Host struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HostSpec   `json:"spec,omitempty"`
	Status HostStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HostList contains a list of Host.
type HostList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Host `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Host{}, &HostList{})
}
