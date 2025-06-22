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
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	tailscalev1alpha1 "github.com/rajsinghtech/tailscale-gateway/api/v1alpha1"
)

// TailscaleGatewayReconciler reconciles a TailscaleGateway object
type TailscaleGatewayReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=tailscale.com,resources=tailscalegateways,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=tailscale.com,resources=tailscalegateways/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=tailscale.com,resources=tailscalegateways/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gatewayclasses,verbs=get;list;watch
//+kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop
func (r *TailscaleGatewayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the TailscaleGateway instance
	var gateway tailscalev1alpha1.TailscaleGateway
	if err := r.Get(ctx, req.NamespacedName, &gateway); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Create ServiceAccount
	if err := r.reconcileServiceAccount(ctx, &gateway); err != nil {
		logger.Error(err, "Failed to reconcile ServiceAccount")
		return ctrl.Result{}, err
	}

	// Create RBAC resources
	if err := r.reconcileRBAC(ctx, &gateway); err != nil {
		logger.Error(err, "Failed to reconcile RBAC")
		return ctrl.Result{}, err
	}

	// Create xDS server deployment
	if err := r.reconcileXDSServer(ctx, &gateway); err != nil {
		logger.Error(err, "Failed to reconcile xDS server")
		return ctrl.Result{}, err
	}

	// Update status
	if err := r.updateStatus(ctx, &gateway); err != nil {
		logger.Error(err, "Failed to update status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// reconcileServiceAccount creates the ServiceAccount for Tailscale proxies
func (r *TailscaleGatewayReconciler) reconcileServiceAccount(ctx context.Context, gateway *tailscalev1alpha1.TailscaleGateway) error {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tailscale",
			Namespace: gateway.Namespace,
		},
	}

	if err := controllerutil.SetControllerReference(gateway, sa, r.Scheme); err != nil {
		return err
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, sa, func() error {
		// No specific configuration needed
		return nil
	})
	return err
}

// reconcileRBAC creates RBAC resources for the operator
func (r *TailscaleGatewayReconciler) reconcileRBAC(ctx context.Context, gateway *tailscalev1alpha1.TailscaleGateway) error {
	// Create Role
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tailscale-operator",
			Namespace: gateway.Namespace,
		},
	}

	if err := controllerutil.SetControllerReference(gateway, role, r.Scheme); err != nil {
		return err
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, role, func() error {
		role.Rules = []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"secrets", "configmaps", "services", "endpoints"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
			{
				APIGroups: []string{"apps"},
				Resources: []string{"statefulsets"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
			{
				APIGroups: []string{"discovery.k8s.io"},
				Resources: []string{"endpointslices"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Create RoleBinding
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tailscale-operator",
			Namespace: gateway.Namespace,
		},
	}

	if err := controllerutil.SetControllerReference(gateway, rb, r.Scheme); err != nil {
		return err
	}

	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, rb, func() error {
		rb.RoleRef = rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     "tailscale-operator",
		}
		rb.Subjects = []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "tailscale",
				Namespace: gateway.Namespace,
			},
		}
		return nil
	})

	return err
}

// reconcileXDSServer creates the xDS extension server deployment
func (r *TailscaleGatewayReconciler) reconcileXDSServer(ctx context.Context, gateway *tailscalev1alpha1.TailscaleGateway) error {
	logger := log.FromContext(ctx)

	// Create Service for xDS server
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-xds", gateway.Name),
			Namespace: gateway.Namespace,
		},
	}

	if err := controllerutil.SetControllerReference(gateway, service, r.Scheme); err != nil {
		return err
	}

	port := int32(8001)
	if gateway.Spec.XDSServer != nil && gateway.Spec.XDSServer.Port != 0 {
		port = gateway.Spec.XDSServer.Port
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, service, func() error {
		service.Spec = corev1.ServiceSpec{
			Selector: map[string]string{
				"app":       "tailscale-gateway",
				"component": "xds-server",
				"gateway":   gateway.Name,
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "grpc",
					Port:       port,
					TargetPort: intstr.FromInt(int(port)),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		}
		return nil
	})
	if err != nil {
		return err
	}
	logger.Info("xDS service", "operation", op)

	// Create Deployment for xDS server
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-xds", gateway.Name),
			Namespace: gateway.Namespace,
		},
	}

	if err := controllerutil.SetControllerReference(gateway, deployment, r.Scheme); err != nil {
		return err
	}

	replicas := int32(1)
	op, err = controllerutil.CreateOrUpdate(ctx, r.Client, deployment, func() error {
		deployment.Spec = appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app":       "tailscale-gateway",
					"component": "xds-server",
					"gateway":   gateway.Name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":       "tailscale-gateway",
						"component": "xds-server",
						"gateway":   gateway.Name,
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: "tailscale-operator",
					Containers: []corev1.Container{
						{
							Name:  "xds-server",
							Image: "ghcr.io/rajsinghtech/tailscale-gateway:latest",
							Args: []string{
								"xds-server",
								fmt.Sprintf("--address=:%d", port),
								fmt.Sprintf("--namespace=%s", gateway.Namespace),
							},
							Ports: []corev1.ContainerPort{
								{
									Name:          "grpc",
									ContainerPort: port,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							Env: []corev1.EnvVar{
								{
									Name:  "GATEWAY_NAME",
									Value: gateway.Name,
								},
							},
						},
					},
				},
			},
		}
		return nil
	})
	if err != nil {
		return err
	}
	logger.Info("xDS deployment", "operation", op)

	return nil
}

// updateStatus updates the TailscaleGateway status
func (r *TailscaleGatewayReconciler) updateStatus(ctx context.Context, gateway *tailscalev1alpha1.TailscaleGateway) error {
	// Check xDS server deployment
	deployment := &appsv1.Deployment{}
	if err := r.Get(ctx, client.ObjectKey{
		Name:      fmt.Sprintf("%s-xds", gateway.Name),
		Namespace: gateway.Namespace,
	}, deployment); err != nil {
		gateway.Status.XDSServerReady = false
	} else {
		gateway.Status.XDSServerReady = deployment.Status.ReadyReplicas > 0
	}

	// Count TailscaleProxies
	var proxies tailscalev1alpha1.TailscaleProxyList
	if err := r.List(ctx, &proxies, client.InNamespace(gateway.Namespace), client.MatchingFields{
		"spec.className": gateway.Name,
	}); err == nil {
		gateway.Status.ProxyCount = int32(len(proxies.Items))
	}

	// Find attached Gateways
	var gateways gwv1.GatewayList
	if err := r.List(ctx, &gateways, client.InNamespace(gateway.Namespace)); err == nil {
		gateway.Status.AttachedGateways = []gwv1.ObjectReference{}
		for _, gw := range gateways.Items {
			if string(gw.Spec.GatewayClassName) == gateway.Spec.GatewayClassName {
				namespace := gwv1.Namespace(gw.Namespace)
				gateway.Status.AttachedGateways = append(gateway.Status.AttachedGateways, gwv1.ObjectReference{
					Group:     gwv1.Group(gw.GroupVersionKind().Group),
					Kind:      gwv1.Kind(gw.GroupVersionKind().Kind),
					Namespace: &namespace,
					Name:      gwv1.ObjectName(gw.Name),
				})
			}
		}
	}

	// Update conditions
	condition := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		ObservedGeneration: gateway.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             "GatewayReady",
		Message:            "TailscaleGateway is ready",
	}

	if !gateway.Status.XDSServerReady {
		condition.Status = metav1.ConditionFalse
		condition.Reason = "XDSServerNotReady"
		condition.Message = "xDS server is not ready"
	}

	gateway.Status.Conditions = []metav1.Condition{condition}

	return r.Status().Update(ctx, gateway)
}

// SetupWithManager sets up the controller with the Manager
func (r *TailscaleGatewayReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Index TailscaleProxies by className for efficient lookup
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &tailscalev1alpha1.TailscaleProxy{}, "spec.className", func(obj client.Object) []string {
		proxy := obj.(*tailscalev1alpha1.TailscaleProxy)
		return []string{proxy.Spec.ClassName}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&tailscalev1alpha1.TailscaleGateway{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Complete(r)
}
