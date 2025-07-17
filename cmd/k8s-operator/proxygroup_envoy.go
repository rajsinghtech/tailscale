// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

//go:build !plan9

package main

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"sort"
	"strings"
	"text/template"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/yaml"
	
	tsapi "tailscale.com/k8s-operator/apis/v1alpha1"
	"tailscale.com/types/ptr"
)

const (
	// Envoy container constants
	envoyContainerName = "envoy"
	envoyAdminPort     = 9901
	envoyHTTPPort      = 8080
	envoyHTTPSPort     = 8443
	
	// Default Envoy image
	defaultEnvoyImage = "envoyproxy/envoy:v1.31-latest"
)

//go:embed envoy-config-template.yaml
var envoyConfigTemplate string

//go:embed envoy-dynamic-config-template.yaml
var envoyDynamicConfigTemplate string

//go:embed envoy-xds-bootstrap-template.yaml
var envoyXDSBootstrapTemplate string

//go:embed envoy-https-config-template.yaml
var envoyHTTPSConfigTemplate string

// EnvoyConfig represents the data needed to generate Envoy configuration
type EnvoyConfigData struct {
	ServiceName   string
	HTTPPort      int
	HTTPSPort     int
	AdminPort     int
	BackendHost   string
	BackendPort   int
	AccessLogging bool
}

// ServiceRoute represents a routing configuration for a service
type ServiceRoute struct {
	Name        string
	Namespace   string
	ClusterIP   string
	Port        int32
	PathPrefix  string
	HostHeader  string
}

// generateEnvoyConfigMap creates a ConfigMap with dynamic routing based on services
func (r *ProxyGroupReconciler) generateEnvoyConfigMap(ctx context.Context, pg *tsapi.ProxyGroup, svcList *corev1.ServiceList) (*corev1.ConfigMap, error) {
	// Collect all services that reference this ProxyGroup
	var routes []ServiceRoute
	for _, svc := range svcList.Items {
		if svc.Annotations[AnnotationProxyGroup] == pg.Name && svc.Annotations[AnnotationExpose] == "true" {
			// Extract routing configuration from annotations
			route := ServiceRoute{
				Name:       svc.Name,
				Namespace:  svc.Namespace,
				ClusterIP:  svc.Spec.ClusterIP,
				PathPrefix: svc.Annotations["tailscale.com/path-prefix"],
				HostHeader: svc.Annotations["tailscale.com/host-header"],
			}
			
			// Use first port if available
			if len(svc.Spec.Ports) > 0 {
				route.Port = svc.Spec.Ports[0].Port
			} else {
				route.Port = 80
			}
			
			// Default path prefix if not specified
			if route.PathPrefix == "" {
				route.PathPrefix = "/"
			}
			
			routes = append(routes, route)
		}
	}
	
	// If no routes found, create a default backend
	if len(routes) == 0 {
		routes = append(routes, ServiceRoute{
			Name:       "default",
			Namespace:  "default",
			ClusterIP:  "127.0.0.1",
			Port:       80,
			PathPrefix: "/",
		})
	}
	
	// Sort routes by path specificity - more specific paths first
	// This ensures /api/v1 matches before /api which matches before /
	sort.Slice(routes, func(i, j int) bool {
		// First sort by path length (longer = more specific)
		if len(routes[i].PathPrefix) != len(routes[j].PathPrefix) {
			return len(routes[i].PathPrefix) > len(routes[j].PathPrefix)
		}
		// Then by whether they have host headers (host header = more specific)
		hasHostI := routes[i].HostHeader != ""
		hasHostJ := routes[j].HostHeader != ""
		if hasHostI != hasHostJ {
			return hasHostI
		}
		// Finally by path prefix alphabetically
		return routes[i].PathPrefix < routes[j].PathPrefix
	})
	
	// Generate Envoy config with dynamic routes
	config, err := r.generateEnvoyConfigWithRoutes(pg, routes)
	if err != nil {
		return nil, fmt.Errorf("failed to generate Envoy config: %w", err)
	}
	
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            fmt.Sprintf("%s-envoy-config", pg.Name),
			Namespace:       r.tsNamespace,
			Labels:          pgLabels(pg.Name, nil),
			OwnerReferences: pgOwnerReference(pg),
		},
		Data: map[string]string{
			"envoy.yaml": config,
		},
	}, nil
}

// DynamicEnvoyConfig represents the data for dynamic Envoy configuration
type DynamicEnvoyConfig struct {
	ServiceName   string
	HTTPPort      int
	HTTPSPort     int
	AdminPort     int
	AccessLogging bool
	Routes        []ServiceRoute
	TLSCertPath   string
	TLSKeyPath    string
	HTTPSRedirect bool
}

// generateEnvoyConfigWithRoutes generates Envoy configuration with dynamic routing
func (r *ProxyGroupReconciler) generateEnvoyConfigWithRoutes(pg *tsapi.ProxyGroup, routes []ServiceRoute) (string, error) {
	// Choose template based on TLS configuration
	var templateStr string
	var templateName string
	
	if pg.Spec.Envoy != nil && pg.Spec.Envoy.TLS != nil && pg.Spec.Envoy.TLS.Enabled {
		templateStr = envoyHTTPSConfigTemplate
		templateName = "envoy-https-config"
	} else {
		templateStr = envoyDynamicConfigTemplate
		templateName = "envoy-dynamic-config"
	}
	
	// Parse the template
	tmpl, err := template.New(templateName).Parse(templateStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse Envoy config template: %w", err)
	}
	
	// Generate the configuration
	data := DynamicEnvoyConfig{
		ServiceName:   pg.Name,
		HTTPPort:      envoyHTTPPort,
		HTTPSPort:     envoyHTTPSPort,
		AdminPort:     envoyAdminPort,
		AccessLogging: true,
		Routes:        routes,
	}
	
	// Add TLS configuration if enabled
	if pg.Spec.Envoy != nil && pg.Spec.Envoy.TLS != nil && pg.Spec.Envoy.TLS.Enabled {
		data.TLSCertPath = "/etc/tailscale-certs/tls.crt"
		data.TLSKeyPath = "/etc/tailscale-certs/tls.key"
		data.HTTPSRedirect = pg.Spec.Envoy.TLS.HTTPSRedirect
	}
	
	var configBuf bytes.Buffer
	if err := tmpl.Execute(&configBuf, data); err != nil {
		return "", fmt.Errorf("failed to render Envoy config: %w", err)
	}
	
	return configBuf.String(), nil
}

// XDSBootstrapConfig represents the data for xDS bootstrap configuration
type XDSBootstrapConfig struct {
	NodeID           string
	ClusterName      string
	Namespace        string
	ProxyGroupName   string
	XDSServer        string
	AdminPort        int
	AccessLogging    bool
}

// generateEnvoyXDSBootstrapConfigMap creates a ConfigMap with xDS bootstrap configuration
func (r *ProxyGroupReconciler) generateEnvoyXDSBootstrapConfigMap(ctx context.Context, pg *tsapi.ProxyGroup) (*corev1.ConfigMap, error) {
	// Parse xDS server address
	xdsAddress := "operator.tailscale.svc.cluster.local:18000" // Default to operator service
	
	if pg.Spec.Envoy != nil && pg.Spec.Envoy.XDSServer != "" {
		xdsAddress = pg.Spec.Envoy.XDSServer
	}
	
	// Generate bootstrap configuration
	tmpl, err := template.New("envoy-xds-bootstrap").Parse(envoyXDSBootstrapTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to parse xDS bootstrap template: %w", err)
	}
	
	data := XDSBootstrapConfig{
		NodeID:           fmt.Sprintf("%s.%s", pg.Name, r.tsNamespace),
		ClusterName:      pg.Name,
		Namespace:        r.tsNamespace,
		ProxyGroupName:   pg.Name,
		XDSServer:        xdsAddress,
		AdminPort:        envoyAdminPort,
		AccessLogging:    true,
	}
	
	var configBuf bytes.Buffer
	if err := tmpl.Execute(&configBuf, data); err != nil {
		return nil, fmt.Errorf("failed to render xDS bootstrap config: %w", err)
	}
	
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            fmt.Sprintf("%s-envoy-config", pg.Name),
			Namespace:       r.tsNamespace,
			Labels:          pgLabels(pg.Name, nil),
			OwnerReferences: pgOwnerReference(pg),
		},
		Data: map[string]string{
			"envoy.yaml": configBuf.String(),
		},
	}, nil
}

// pgEnvoyCM creates a ConfigMap for Envoy configuration (deprecated, kept for compatibility)
func pgEnvoyCM(pg *tsapi.ProxyGroup, namespace string) (*corev1.ConfigMap, error) {
	// This function is now deprecated in favor of generateEnvoyConfigMap
	return nil, fmt.Errorf("pgEnvoyCM is deprecated, use generateEnvoyConfigMap instead")
}

// envoyContainer creates the Envoy container for the ProxyGroup pod
func envoyContainer(pg *tsapi.ProxyGroup) corev1.Container {
	image := defaultEnvoyImage
	if pg.Spec.Envoy != nil && pg.Spec.Envoy.Image != "" {
		image = pg.Spec.Envoy.Image
	}
	
	// Wait for tailscale socket before starting envoy
	command := []string{
		"sh", "-c",
		"while [ ! -e /tmp/tailscaled.sock ]; do echo 'Waiting for tailscale...'; sleep 2; done; envoy -c /etc/envoy/envoy.yaml --service-cluster " + pg.Name + " --service-node $(POD_NAME)",
	}
	
	// Add extra args if specified (append to the command)
	if pg.Spec.Envoy != nil && len(pg.Spec.Envoy.ExtraArgs) > 0 {
		extraArgs := strings.Join(pg.Spec.Envoy.ExtraArgs, " ")
		command[2] = command[2] + " " + extraArgs
	}
	
	container := corev1.Container{
		Name:    envoyContainerName,
		Image:   image,
		Command: command,
		Env: []corev1.EnvVar{
			{
				Name: "POD_NAME",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "metadata.name",
					},
				},
			},
			{
				Name: "POD_NAMESPACE",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "metadata.namespace",
					},
				},
			},
		},
		Ports: []corev1.ContainerPort{
			{Name: "http", ContainerPort: envoyHTTPPort, Protocol: corev1.ProtocolTCP},
			{Name: "https", ContainerPort: envoyHTTPSPort, Protocol: corev1.ProtocolTCP},
			{Name: "admin", ContainerPort: envoyAdminPort, Protocol: corev1.ProtocolTCP},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "envoy-config", MountPath: "/etc/envoy", ReadOnly: true},
			{Name: "tsstate", MountPath: "/tmp"},
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/ready",
					Port: intstr.FromInt(envoyAdminPort),
				},
			},
			InitialDelaySeconds: 10,
			PeriodSeconds:       5,
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/server_info",
					Port: intstr.FromInt(envoyAdminPort),
				},
			},
			InitialDelaySeconds: 30,
			PeriodSeconds:       10,
		},
	}
	
	// Apply resource requirements if specified
	if pg.Spec.Envoy != nil && pg.Spec.Envoy.Resources != nil {
		container.Resources = *pg.Spec.Envoy.Resources
	}
	
	// Add TLS certificate volume mount if TLS is enabled
	if pg.Spec.Envoy != nil && pg.Spec.Envoy.TLS != nil && pg.Spec.Envoy.TLS.Enabled {
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      "tailscale-certs",
			MountPath: "/etc/tailscale-certs",
			ReadOnly:  true,
		})
	}
	
	return container
}


// reconcileEnvoyConfigMap ensures the Envoy ConfigMap exists and is up to date
func (r *ProxyGroupReconciler) reconcileEnvoyConfigMap(ctx context.Context, pg *tsapi.ProxyGroup) error {
	// Check if we're using xDS mode
	if pg.Spec.Envoy != nil && pg.Spec.Envoy.ConfigSource == "xds" {
		return r.reconcileEnvoyXDSConfig(ctx, pg)
	}
	
	// Static mode - use ConfigMap
	// Find services that reference this ProxyGroup
	svcList := &corev1.ServiceList{}
	if err := r.List(ctx, svcList); err != nil {
		return fmt.Errorf("error listing services: %w", err)
	}
	
	// Generate ConfigMap with all backend services
	cm, err := r.generateEnvoyConfigMap(ctx, pg, svcList)
	if err != nil {
		return fmt.Errorf("error generating Envoy ConfigMap: %w", err)
	}
	
	if _, err := createOrUpdate(ctx, r.Client, r.tsNamespace, cm, func(existing *corev1.ConfigMap) {
		existing.ObjectMeta.Labels = cm.ObjectMeta.Labels
		existing.ObjectMeta.OwnerReferences = cm.ObjectMeta.OwnerReferences
		existing.Data = cm.Data
	}); err != nil {
		return fmt.Errorf("error provisioning Envoy ConfigMap %q: %w", cm.Name, err)
	}
	
	return nil
}

// reconcileEnvoyXDSConfig configures Envoy to use xDS for dynamic configuration
func (r *ProxyGroupReconciler) reconcileEnvoyXDSConfig(ctx context.Context, pg *tsapi.ProxyGroup) error {
	// Create bootstrap ConfigMap for xDS mode
	cm, err := r.generateEnvoyXDSBootstrapConfigMap(ctx, pg)
	if err != nil {
		return fmt.Errorf("error generating xDS bootstrap ConfigMap: %w", err)
	}
	
	if _, err := createOrUpdate(ctx, r.Client, r.tsNamespace, cm, func(existing *corev1.ConfigMap) {
		existing.ObjectMeta.Labels = cm.ObjectMeta.Labels
		existing.ObjectMeta.OwnerReferences = cm.ObjectMeta.OwnerReferences
		existing.Data = cm.Data
	}); err != nil {
		return fmt.Errorf("error provisioning xDS bootstrap ConfigMap %q: %w", cm.Name, err)
	}
	
	// Update xDS server with current configuration
	if r.envoyXDSServer != nil {
		svcList := &corev1.ServiceList{}
		if err := r.List(ctx, svcList); err != nil {
			return fmt.Errorf("error listing services: %w", err)
		}
		
		// Collect routes from services
		var routes []ServiceRoute
		for _, svc := range svcList.Items {
			if svc.Annotations[AnnotationProxyGroup] == pg.Name && svc.Annotations[AnnotationExpose] == "true" {
				route := ServiceRoute{
					Name:       svc.Name,
					Namespace:  svc.Namespace,
					ClusterIP:  svc.Spec.ClusterIP,
					PathPrefix: svc.Annotations["tailscale.com/path-prefix"],
					HostHeader: svc.Annotations["tailscale.com/host-header"],
				}
				
				if len(svc.Spec.Ports) > 0 {
					route.Port = svc.Spec.Ports[0].Port
				} else {
					route.Port = 80
				}
				
				if route.PathPrefix == "" {
					route.PathPrefix = "/"
				}
				
				routes = append(routes, route)
			}
		}
		
		// Sort routes by specificity
		sort.Slice(routes, func(i, j int) bool {
			if len(routes[i].PathPrefix) != len(routes[j].PathPrefix) {
				return len(routes[i].PathPrefix) > len(routes[j].PathPrefix)
			}
			hasHostI := routes[i].HostHeader != ""
			hasHostJ := routes[j].HostHeader != ""
			if hasHostI != hasHostJ {
				return hasHostI
			}
			return routes[i].PathPrefix < routes[j].PathPrefix
		})
		
		// Update xDS configuration
		if err := r.envoyXDSServer.UpdateConfiguration(ctx, pg, routes); err != nil {
			return fmt.Errorf("error updating xDS configuration: %w", err)
		}
	}
	
	return nil
}

// envoyStatefulSet creates a StatefulSet for the Envoy ProxyGroup
// by using the standard proxy template and adding Envoy-specific configuration
func envoyStatefulSet(pg *tsapi.ProxyGroup, namespace, image, tsFirewallMode string, port *uint16, proxyClass *tsapi.ProxyClass) (*appsv1.StatefulSet, error) {
	// Start with the standard proxy template
	ss := new(appsv1.StatefulSet)
	if err := yaml.Unmarshal(proxyYaml, &ss); err != nil {
		return nil, fmt.Errorf("failed to unmarshal proxy spec: %w", err)
	}
	
	// Validate base assumptions
	if len(ss.Spec.Template.Spec.InitContainers) != 1 {
		return nil, fmt.Errorf("[unexpected] base proxy config had %d init containers instead of 1", len(ss.Spec.Template.Spec.InitContainers))
	}
	if len(ss.Spec.Template.Spec.Containers) != 1 {
		return nil, fmt.Errorf("[unexpected] base proxy config had %d containers instead of 1", len(ss.Spec.Template.Spec.Containers))
	}

	// StatefulSet config
	ss.ObjectMeta = metav1.ObjectMeta{
		Name:            pg.Name,
		Namespace:       namespace,
		Labels:          pgLabels(pg.Name, nil),
		OwnerReferences: pgOwnerReference(pg),
	}
	ss.Spec.Replicas = ptr.To(pgReplicas(pg))
	ss.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: pgLabels(pg.Name, nil),
	}

	// Pod template config
	pod := &ss.Spec.Template
	pod.ObjectMeta = metav1.ObjectMeta{
		Name:                       pg.Name,
		Namespace:                  namespace,
		Labels:                     pgLabels(pg.Name, nil),
		DeletionGracePeriodSeconds: ptr.To(deletionGracePeriodSeconds),
	}
	pod.Spec.ServiceAccountName = pg.Name
	pod.Spec.PriorityClassName = "system-cluster-critical"

	// Update the init container image
	for i := range ss.Spec.Template.Spec.InitContainers {
		c := &ss.Spec.Template.Spec.InitContainers[i]
		if c.Name == "sysctler" {
			c.Image = image
			break
		}
	}
	
	// Update the tailscale container
	tsContainer := &ss.Spec.Template.Spec.Containers[0]
	tsContainer.Image = image
	
	// Add volume mounts for tailscale configs
	for i := range pgReplicas(pg) {
		tsContainer.VolumeMounts = append(tsContainer.VolumeMounts, corev1.VolumeMount{
			Name:      fmt.Sprintf("tailscaledconfig-%d", i),
			ReadOnly:  true,
			MountPath: fmt.Sprintf("/etc/tsconfig/%s-%d", pg.Name, i),
		})
	}
	
	// Add ProxyGroup-specific environment variables
	// Note: We need to add these env vars that are normally added by pgStatefulSet
	tsContainer.Env = append(tsContainer.Env,
		corev1.EnvVar{
			// TODO(irbekrm): verify that .status.podIPs are always set, else read in .status.podIP as well.
			Name: "POD_IPS", // this will be a comma separate list i.e 10.136.0.6,2600:1900:4011:161:0:e:0:6
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "status.podIPs",
				},
			},
		},
		corev1.EnvVar{
			Name:  "TS_KUBE_SECRET",
			Value: "$(POD_NAME)",
		},
		corev1.EnvVar{
			// TODO(tomhjp): This is tsrecorder-specific and does nothing. Delete.
			Name:  "TS_STATE",
			Value: "kube:$(POD_NAME)",
		},
		corev1.EnvVar{
			Name:  "TS_EXPERIMENTAL_VERSIONED_CONFIG_DIR",
			Value: "/etc/tsconfig/$(POD_NAME)",
		},
		corev1.EnvVar{
			Name:  "TS_INTERNAL_APP",
			Value: "envoy",
		},
	)

	// Add volume mount for tailscale tmp directory so envoy can access the socket
	tsContainer.VolumeMounts = append(tsContainer.VolumeMounts, corev1.VolumeMount{
		Name:      "tsstate",
		MountPath: "/tmp",
	})
	
	// Add Envoy container
	envoyContainer := envoyContainer(pg)
	ss.Spec.Template.Spec.Containers = append(ss.Spec.Template.Spec.Containers, envoyContainer)

	// Add volumes for tailscale configs
	for i := range pgReplicas(pg) {
		vol := corev1.Volume{
			Name: fmt.Sprintf("tailscaledconfig-%d", i),
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: pgConfigSecretName(pg.Name, i),
				},
			},
		}
		ss.Spec.Template.Spec.Volumes = append(ss.Spec.Template.Spec.Volumes, vol)
	}
	
	// Add tsstate volume for sharing tailscale socket between containers
	tsstateVolume := corev1.Volume{
		Name: "tsstate",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	}
	ss.Spec.Template.Spec.Volumes = append(ss.Spec.Template.Spec.Volumes, tsstateVolume)
	
	// Add Envoy config volume
	envoyConfigVolume := corev1.Volume{
		Name: "envoy-config",
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: fmt.Sprintf("%s-envoy-config", pg.Name),
				},
			},
		},
	}
	ss.Spec.Template.Spec.Volumes = append(ss.Spec.Template.Spec.Volumes, envoyConfigVolume)
	
	// Add TLS certificate volume if TLS is enabled
	if pg.Spec.Envoy != nil && pg.Spec.Envoy.TLS != nil && pg.Spec.Envoy.TLS.Enabled {
		var tlsVolume corev1.Volume
		
		if pg.Spec.Envoy.TLS.CertSource == "user" && pg.Spec.Envoy.TLS.CertSecretName != "" {
			// Use user-provided secret
			tlsVolume = corev1.Volume{
				Name: "tailscale-certs",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: pg.Spec.Envoy.TLS.CertSecretName,
					},
				},
			}
		} else {
			// Use Tailscale-generated certificates (default)
			tlsVolume = corev1.Volume{
				Name: "tailscale-certs",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: fmt.Sprintf("%s-tls", pg.Name),
					},
				},
			}
		}
		
		ss.Spec.Template.Spec.Volumes = append(ss.Spec.Template.Spec.Volumes, tlsVolume)
	}

	return ss, nil
}