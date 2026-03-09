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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// HostSpec defines the desired state of Host.
type HostSpec struct {
	// NodeID is the Ironic node UUID (or name) this Host represents. Required for power management.
	// +kubebuilder:validation:Required
	NodeID string `json:"nodeID"`
	// NetworkConfig specifies host networking (e.g. which network/VLAN to attach).
	NetworkConfig *NetworkConfig `json:"networkConfig,omitempty"`
	// Online is the desired power state (true = on, false = off).
	Online bool `json:"online"`
	// ConsumerRef references the cluster that is using this host.
	ConsumerRef *corev1.ObjectReference `json:"consumerRef,omitempty"`
	// Image to provision; when set, triggers provisioning. When cleared, triggers deprovisioning.
	Image *Image `json:"image,omitempty"`
}

// NetworkConfig holds host network configuration.
type NetworkConfig struct {
	// Network identifies the network (e.g. private-vlan-network) the host should use.
	Network string `json:"network"`
}

// Image holds the image to provision.
type Image struct {
	URL    string `json:"url"`
	Format string `json:"format,omitempty"` // e.g. live-iso, raw, qcow2
}

// ProvisioningState defines the states the provisioner will report
// the host has having.
type ProvisioningState string

const (
	StateUnmanaged      ProvisioningState = "unmanaged"
	StateAvailable      ProvisioningState = "available"
	StateProvisioning   ProvisioningState = "provisioning"
	StateProvisioned    ProvisioningState = "provisioned"
	StateDeprovisioning ProvisioningState = "deprovisioning"
)

// ProvisionStatus holds current provisioning state from the backend.
type ProvisionStatus struct {
	// ID is the host ID in the Bare Metal Management system.
	ID string `json:"id,omitempty"`
	// Image is the currently provisioned image (if any).
	Image Image `json:"image,omitempty"`
	// State is the current provisioning state.
	State ProvisioningState `json:"state,omitempty"`
}

// HostStatus defines the observed state of Host.
type HostStatus struct {
	// PoweredOn is the current power state.
	PoweredOn *bool `json:"poweredOn,omitempty"`
	// Provisioning holds ID, current image, and state from the backend.
	Provisioning ProvisionStatus `json:"provisioning,omitempty"`
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
