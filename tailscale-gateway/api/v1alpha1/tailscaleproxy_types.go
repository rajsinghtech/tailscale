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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TailscaleProxySpec defines the desired state of TailscaleProxy
type TailscaleProxySpec struct {
	// Type defines whether this is an ingress or egress proxy
	// +kubebuilder:validation:Enum=ingress;egress
	Type ProxyType `json:"type"`

	// Replicas is the number of proxy instances to run
	// +kubebuilder:default=2
	// +kubebuilder:validation:Minimum=1
	Replicas int32 `json:"replicas,omitempty"`

	// ClassName specifies which TailscaleGateway this proxy belongs to
	ClassName string `json:"className"`

	// IngressConfig is required when Type is "ingress"
	// +optional
	IngressConfig *IngressConfig `json:"ingressConfig,omitempty"`

	// EgressConfig is required when Type is "egress"
	// +optional
	EgressConfig *EgressConfig `json:"egressConfig,omitempty"`

	// Tags are the ACL tags to apply to the Tailscale nodes
	// +optional
	Tags []string `json:"tags,omitempty"`
}

// ProxyType defines the type of proxy
type ProxyType string

const (
	ProxyTypeIngress ProxyType = "ingress"
	ProxyTypeEgress  ProxyType = "egress"
)

// IngressConfig defines configuration for ingress proxies
type IngressConfig struct {
	// Hostname is the Tailscale hostname for the ingress
	Hostname string `json:"hostname"`

	// Services defines the services to expose via Tailscale ingress
	Services []IngressService `json:"services"`

	// UseFunnel enables Tailscale Funnel for public access
	// +optional
	UseFunnel bool `json:"useFunnel,omitempty"`
}

// IngressService defines a service to expose via ingress
type IngressService struct {
	// Name is the service name
	Name string `json:"name"`

	// Protocol is the protocol (http, https, tcp)
	// +kubebuilder:validation:Enum=http;https;tcp
	Protocol string `json:"protocol"`

	// Port is the port to expose
	Port int32 `json:"port"`

	// TargetPort is the target port on the backend service
	TargetPort int32 `json:"targetPort"`

	// Path is the path prefix for HTTP/HTTPS services
	// +optional
	Path string `json:"path,omitempty"`
}

// EgressConfig defines configuration for egress proxies
type EgressConfig struct {
	// Services defines the Tailscale services to access from the cluster
	Services []EgressService `json:"services"`
}

// EgressService defines a Tailscale service to access via egress
type EgressService struct {
	// Name is the service name to create in the cluster
	Name string `json:"name"`

	// TailscaleTarget is the Tailscale hostname or IP to connect to
	TailscaleTarget string `json:"tailscaleTarget"`

	// Port is the port on the Tailscale target
	Port int32 `json:"port"`

	// Protocol is the protocol (tcp, udp)
	// +kubebuilder:validation:Enum=tcp;udp
	// +kubebuilder:default=tcp
	Protocol string `json:"protocol,omitempty"`
}

// TailscaleProxyStatus defines the observed state of TailscaleProxy
type TailscaleProxyStatus struct {
	// Conditions describe the current state of the TailscaleProxy
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ReadyReplicas is the number of ready proxy instances
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// Devices lists the Tailscale device information for the proxies
	Devices []DeviceInfo `json:"devices,omitempty"`
}

// DeviceInfo contains information about a Tailscale device
type DeviceInfo struct {
	// Hostname is the Tailscale hostname
	Hostname string `json:"hostname"`

	// TailscaleIP is the Tailscale IP address
	TailscaleIP string `json:"tailscaleIP"`

	// PodName is the name of the pod running this device
	PodName string `json:"podName"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=tsp
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Ready",type="integer",JSONPath=".status.readyReplicas"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// TailscaleProxy is the Schema for the tailscaleproxies API
type TailscaleProxy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TailscaleProxySpec   `json:"spec,omitempty"`
	Status TailscaleProxyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TailscaleProxyList contains a list of TailscaleProxy
type TailscaleProxyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TailscaleProxy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TailscaleProxy{}, &TailscaleProxyList{})
}