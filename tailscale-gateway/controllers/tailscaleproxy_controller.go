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

package controllers

import (
	"context"
	"encoding/json"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	tailscalev1alpha1 "github.com/rajsinghtech/tailscale-gateway/api/v1alpha1"
)

// TailscaleProxyReconciler reconciles a TailscaleProxy object
type TailscaleProxyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=tailscale.com,resources=tailscaleproxies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=tailscale.com,resources=tailscaleproxies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=tailscale.com,resources=tailscaleproxies/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
//+kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop
func (r *TailscaleProxyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the TailscaleProxy instance
	var proxy tailscalev1alpha1.TailscaleProxy
	if err := r.Get(ctx, req.NamespacedName, &proxy); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Get the associated TailscaleGateway
	var gateway tailscalev1alpha1.TailscaleGateway
	if err := r.Get(ctx, types.NamespacedName{
		Name:      proxy.Spec.ClassName,
		Namespace: proxy.Namespace,
	}, &gateway); err != nil {
		logger.Error(err, "Failed to get TailscaleGateway", "className", proxy.Spec.ClassName)
		return ctrl.Result{}, err
	}

	// Create or update resources based on proxy type
	switch proxy.Spec.Type {
	case tailscalev1alpha1.ProxyTypeIngress:
		if err := r.reconcileIngressProxy(ctx, &proxy, &gateway); err != nil {
			return ctrl.Result{}, err
		}
	case tailscalev1alpha1.ProxyTypeEgress:
		if err := r.reconcileEgressProxy(ctx, &proxy, &gateway); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Update status
	if err := r.updateStatus(ctx, &proxy); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// reconcileIngressProxy creates resources for ingress proxy
func (r *TailscaleProxyReconciler) reconcileIngressProxy(ctx context.Context, proxy *tailscalev1alpha1.TailscaleProxy, gateway *tailscalev1alpha1.TailscaleGateway) error {
	logger := log.FromContext(ctx)

	// Create serve config ConfigMap
	serveConfig, err := r.createIngressServeConfig(proxy)
	if err != nil {
		return err
	}

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-serve-config", proxy.Name),
			Namespace: proxy.Namespace,
		},
	}

	if err := controllerutil.SetControllerReference(proxy, configMap, r.Scheme); err != nil {
		return err
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, configMap, func() error {
		configMap.Data = map[string]string{
			"serve-config.json": string(serveConfig),
		}
		return nil
	})
	if err != nil {
		return err
	}
	logger.Info("Ingress serve config", "operation", op)

	// Create headless service
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-ingress", proxy.Name),
			Namespace: proxy.Namespace,
		},
	}

	if err := controllerutil.SetControllerReference(proxy, service, r.Scheme); err != nil {
		return err
	}

	op, err = controllerutil.CreateOrUpdate(ctx, r.Client, service, func() error {
		service.Spec = corev1.ServiceSpec{
			ClusterIP: "None", // Headless service
			Selector: map[string]string{
				"app":  "tailscale-proxy",
				"type": "ingress",
				"name": proxy.Name,
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "metrics",
					Port:       9002,
					TargetPort: intstr.FromInt(9002),
				},
			},
		}
		return nil
	})
	if err != nil {
		return err
	}
	logger.Info("Ingress service", "operation", op)

	// Create StatefulSet
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-ingress", proxy.Name),
			Namespace: proxy.Namespace,
		},
	}

	if err := controllerutil.SetControllerReference(proxy, sts, r.Scheme); err != nil {
		return err
	}

	op, err = controllerutil.CreateOrUpdate(ctx, r.Client, sts, func() error {
		sts.Spec = r.createIngressStatefulSetSpec(proxy, gateway)
		return nil
	})
	if err != nil {
		return err
	}
	logger.Info("Ingress StatefulSet", "operation", op)

	return nil
}

// reconcileEgressProxy creates resources for egress proxy
func (r *TailscaleProxyReconciler) reconcileEgressProxy(ctx context.Context, proxy *tailscalev1alpha1.TailscaleProxy, gateway *tailscalev1alpha1.TailscaleGateway) error {
	logger := log.FromContext(ctx)

	// Create egress services and EndpointSlices
	for _, svc := range proxy.Spec.EgressConfig.Services {
		// Create ClusterIP service without selector
		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      svc.Name,
				Namespace: proxy.Namespace,
			},
		}

		if err := controllerutil.SetControllerReference(proxy, service, r.Scheme); err != nil {
			return err
		}

		op, err := controllerutil.CreateOrUpdate(ctx, r.Client, service, func() error {
			service.Spec = corev1.ServiceSpec{
				Type:      corev1.ServiceTypeClusterIP,
				ClusterIP: corev1.ClusterIPNone,
				Ports: []corev1.ServicePort{
					{
						Name:       "main",
						Port:       svc.Port,
						TargetPort: intstr.FromInt(int(svc.Port)),
						Protocol:   corev1.Protocol(svc.Protocol),
					},
				},
			}
			return nil
		})
		if err != nil {
			return err
		}
		logger.Info("Egress service", "service", svc.Name, "operation", op)
	}

	// Create headless service for the egress proxy pods
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-egress", proxy.Name),
			Namespace: proxy.Namespace,
		},
	}

	if err := controllerutil.SetControllerReference(proxy, service, r.Scheme); err != nil {
		return err
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, service, func() error {
		service.Spec = corev1.ServiceSpec{
			ClusterIP: "None", // Headless service
			Selector: map[string]string{
				"app":  "tailscale-proxy",
				"type": "egress",
				"name": proxy.Name,
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "metrics",
					Port:       9002,
					TargetPort: intstr.FromInt(9002),
				},
			},
		}
		return nil
	})
	if err != nil {
		return err
	}
	logger.Info("Egress proxy service", "operation", op)

	// Create StatefulSet
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-egress", proxy.Name),
			Namespace: proxy.Namespace,
		},
	}

	if err := controllerutil.SetControllerReference(proxy, sts, r.Scheme); err != nil {
		return err
	}

	op, err = controllerutil.CreateOrUpdate(ctx, r.Client, sts, func() error {
		sts.Spec = r.createEgressStatefulSetSpec(proxy, gateway)
		return nil
	})
	if err != nil {
		return err
	}
	logger.Info("Egress StatefulSet", "operation", op)

	return nil
}

// createIngressServeConfig creates the serve configuration for ingress
func (r *TailscaleProxyReconciler) createIngressServeConfig(proxy *tailscalev1alpha1.TailscaleProxy) ([]byte, error) {
	config := map[string]interface{}{
		"TCP": make(map[string]interface{}),
	}

	if proxy.Spec.IngressConfig == nil {
		return json.Marshal(config)
	}

	for _, svc := range proxy.Spec.IngressConfig.Services {
		if svc.Protocol == "tcp" {
			config["TCP"].(map[string]interface{})[fmt.Sprint(svc.Port)] = fmt.Sprintf("tcp://localhost:%d", svc.TargetPort)
		} else if svc.Protocol == "http" || svc.Protocol == "https" {
			if _, ok := config["Web"]; !ok {
				config["Web"] = make(map[string]interface{})
			}
			path := svc.Path
			if path == "" {
				path = "/"
			}
			config["Web"].(map[string]interface{})[fmt.Sprintf("%s:%d%s", proxy.Spec.IngressConfig.Hostname, svc.Port, path)] = map[string]interface{}{
				"Handlers": map[string]interface{}{
					path: map[string]interface{}{
						"Proxy": fmt.Sprintf("http://localhost:%d", svc.TargetPort),
					},
				},
			}
		}
	}

	return json.MarshalIndent(config, "", "  ")
}

// createIngressStatefulSetSpec creates the StatefulSet spec for ingress
func (r *TailscaleProxyReconciler) createIngressStatefulSetSpec(proxy *tailscalev1alpha1.TailscaleProxy, gateway *tailscalev1alpha1.TailscaleGateway) appsv1.StatefulSetSpec {
	replicas := proxy.Spec.Replicas
	if replicas == 0 {
		replicas = 2
	}

	labels := map[string]string{
		"app":  "tailscale-proxy",
		"type": "ingress",
		"name": proxy.Name,
	}

	return appsv1.StatefulSetSpec{
		Replicas:    &replicas,
		ServiceName: fmt.Sprintf("%s-ingress", proxy.Name),
		Selector: &metav1.LabelSelector{
			MatchLabels: labels,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: labels,
			},
			Spec: corev1.PodSpec{
				ServiceAccountName: "tailscale",
				InitContainers: []corev1.Container{
					{
						Name:  "tailscale-init",
						Image: gateway.Spec.ProxyImage,
						Command: []string{
							"sh",
							"-c",
							"tailscale up --authkey=$(TS_AUTH_KEY) --hostname=$(HOSTNAME) --accept-routes",
						},
						Env: r.createTailscaleEnv(proxy, gateway, true),
					},
				},
				Containers: []corev1.Container{
					{
						Name:  "tailscale",
						Image: gateway.Spec.ProxyImage,
						Env:   r.createTailscaleEnv(proxy, gateway, true),
						SecurityContext: &corev1.SecurityContext{
							Capabilities: &corev1.Capabilities{
								Add: []corev1.Capability{"NET_ADMIN"},
							},
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "serve-config",
								MountPath: "/etc/proxies",
								ReadOnly:  true,
							},
							{
								Name:      "state",
								MountPath: "/var/lib/tailscale",
							},
						},
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "serve-config",
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: fmt.Sprintf("%s-serve-config", proxy.Name),
								},
							},
						},
					},
				},
			},
		},
		VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "state",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{
						corev1.ReadWriteOnce,
					},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("1Gi"),
						},
					},
				},
			},
		},
	}
}

// createEgressStatefulSetSpec creates the StatefulSet spec for egress
func (r *TailscaleProxyReconciler) createEgressStatefulSetSpec(proxy *tailscalev1alpha1.TailscaleProxy, gateway *tailscalev1alpha1.TailscaleGateway) appsv1.StatefulSetSpec {
	replicas := proxy.Spec.Replicas
	if replicas == 0 {
		replicas = 2
	}

	labels := map[string]string{
		"app":  "tailscale-proxy",
		"type": "egress",
		"name": proxy.Name,
	}

	return appsv1.StatefulSetSpec{
		Replicas:    &replicas,
		ServiceName: fmt.Sprintf("%s-egress", proxy.Name),
		Selector: &metav1.LabelSelector{
			MatchLabels: labels,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: labels,
			},
			Spec: corev1.PodSpec{
				ServiceAccountName: "tailscale",
				InitContainers: []corev1.Container{
					{
						Name:  "tailscale-init",
						Image: gateway.Spec.ProxyImage,
						Command: []string{
							"sh",
							"-c",
							"tailscale up --authkey=$(TS_AUTH_KEY) --hostname=$(HOSTNAME) --accept-routes",
						},
						Env: r.createTailscaleEnv(proxy, gateway, false),
					},
				},
				Containers: []corev1.Container{
					{
						Name:  "tailscale",
						Image: gateway.Spec.ProxyImage,
						Env:   r.createTailscaleEnv(proxy, gateway, false),
						SecurityContext: &corev1.SecurityContext{
							Capabilities: &corev1.Capabilities{
								Add: []corev1.Capability{"NET_ADMIN"},
							},
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "state",
								MountPath: "/var/lib/tailscale",
							},
						},
					},
				},
			},
		},
		VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "state",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{
						corev1.ReadWriteOnce,
					},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("1Gi"),
						},
					},
				},
			},
		},
	}
}

// createTailscaleEnv creates environment variables for Tailscale containers
func (r *TailscaleProxyReconciler) createTailscaleEnv(proxy *tailscalev1alpha1.TailscaleProxy, gateway *tailscalev1alpha1.TailscaleGateway, isIngress bool) []corev1.EnvVar {
	env := []corev1.EnvVar{
		{
			Name: "TS_AUTH_KEY",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: gateway.Spec.AuthKey.Name,
					},
					Key: gateway.Spec.AuthKey.Key,
				},
			},
		},
		{
			Name: "HOSTNAME",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.name",
				},
			},
		},
		{
			Name:  "TS_USERSPACE",
			Value: "false",
		},
	}

	if isIngress {
		env = append(env, corev1.EnvVar{
			Name:  "TS_EXPERIMENTAL_CERT_SHARE",
			Value: "true",
		})
		env = append(env, corev1.EnvVar{
			Name:  "TS_INGRESS_PROXIES_CONFIG_PATH",
			Value: "/etc/proxies/serve-config.json",
		})
	}

	// Add tags
	if len(proxy.Spec.Tags) > 0 || len(gateway.Spec.Tags) > 0 {
		tags := append(gateway.Spec.Tags, proxy.Spec.Tags...)
		tagsStr := ""
		for i, tag := range tags {
			if i > 0 {
				tagsStr += ","
			}
			tagsStr += tag
		}
		env = append(env, corev1.EnvVar{
			Name:  "TS_TAGS",
			Value: tagsStr,
		})
	}

	return env
}

// updateStatus updates the TailscaleProxy status
func (r *TailscaleProxyReconciler) updateStatus(ctx context.Context, proxy *tailscalev1alpha1.TailscaleProxy) error {
	// Get pods for this proxy
	var pods corev1.PodList
	proxyType := string(proxy.Spec.Type)
	if err := r.List(ctx, &pods, client.InNamespace(proxy.Namespace), client.MatchingLabels{
		"app":  "tailscale-proxy",
		"type": proxyType,
		"name": proxy.Name,
	}); err != nil {
		return err
	}

	// Count ready pods
	readyCount := int32(0)
	devices := []tailscalev1alpha1.DeviceInfo{}
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodRunning {
			readyCount++
			// In a real implementation, we would query Tailscale API for device info
			devices = append(devices, tailscalev1alpha1.DeviceInfo{
				PodName:  pod.Name,
				Hostname: pod.Name,
			})
		}
	}

	proxy.Status.ReadyReplicas = readyCount
	proxy.Status.Devices = devices

	// Update conditions
	condition := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		ObservedGeneration: proxy.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             "ProxyReady",
		Message:            fmt.Sprintf("%d/%d replicas ready", readyCount, proxy.Spec.Replicas),
	}

	if readyCount < proxy.Spec.Replicas {
		condition.Status = metav1.ConditionFalse
		condition.Reason = "InsufficientReplicas"
	}

	proxy.Status.Conditions = []metav1.Condition{condition}

	return r.Status().Update(ctx, proxy)
}

// SetupWithManager sets up the controller with the Manager
func (r *TailscaleProxyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&tailscalev1alpha1.TailscaleProxy{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Complete(r)
}
