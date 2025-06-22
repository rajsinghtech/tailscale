/*
Copyright 2024 Raj Singh.

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
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TailscaleGatewaySpec defines the desired state of TailscaleGateway
type TailscaleGatewaySpec struct {
	// GatewayClassName is the name of the GatewayClass to integrate with
	GatewayClassName string `json:"gatewayClassName"`

	// AuthKey is a reference to a secret containing the Tailscale auth key
	AuthKey AuthKeyReference `json:"authKey"`

	// XDSServer configures the xDS extension server
	// +optional
	XDSServer *XDSServerConfig `json:"xdsServer,omitempty"`

	// ProxyImage is the container image to use for Tailscale proxies
	// +kubebuilder:default="tailscale/tailscale:latest"
	ProxyImage string `json:"proxyImage,omitempty"`

	// Tags are default ACL tags to apply to all proxies
	// +optional
	Tags []string `json:"tags,omitempty"`
}

// AuthKeyReference references a secret containing Tailscale auth key
type AuthKeyReference struct {
	// Name is the name of the secret
	Name string `json:"name"`

	// Key is the key in the secret containing the auth key
	// +kubebuilder:default="authkey"
	Key string `json:"key,omitempty"`
}

// XDSServerConfig configures the xDS extension server
type XDSServerConfig struct {
	// Port is the port the xDS server listens on
	// +kubebuilder:default=8001
	Port int32 `json:"port,omitempty"`

	// ExtensionHooks defines which Envoy Gateway hooks to use
	ExtensionHooks []ExtensionHook `json:"extensionHooks,omitempty"`
}

// ExtensionHook defines an Envoy Gateway extension hook
type ExtensionHook struct {
	// Name is the hook name (Route, VirtualHost, HTTPListener, Translation)
	// +kubebuilder:validation:Enum=Route;VirtualHost;HTTPListener;Translation
	Name string `json:"name"`

	// Priority controls the order of extension execution
	// +kubebuilder:default=0
	Priority int32 `json:"priority,omitempty"`
}

// TailscaleGatewayStatus defines the observed state of TailscaleGateway
type TailscaleGatewayStatus struct {
	// Conditions describe the current state of the TailscaleGateway
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// XDSServerReady indicates if the xDS server is ready
	XDSServerReady bool `json:"xdsServerReady,omitempty"`

	// ProxyCount is the total number of managed proxies
	ProxyCount int32 `json:"proxyCount,omitempty"`

	// AttachedGateways lists the Gateways using this TailscaleGateway
	AttachedGateways []gwv1.ObjectReference `json:"attachedGateways,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=tsgw
// +kubebuilder:printcolumn:name="GatewayClass",type="string",JSONPath=".spec.gatewayClassName"
// +kubebuilder:printcolumn:name="XDS Ready",type="boolean",JSONPath=".status.xdsServerReady"
// +kubebuilder:printcolumn:name="Proxies",type="integer",JSONPath=".status.proxyCount"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// TailscaleGateway is the Schema for the tailscalegateways API
type TailscaleGateway struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TailscaleGatewaySpec   `json:"spec,omitempty"`
	Status TailscaleGatewayStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TailscaleGatewayList contains a list of TailscaleGateway
type TailscaleGatewayList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TailscaleGateway `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TailscaleGateway{}, &TailscaleGatewayList{})
}