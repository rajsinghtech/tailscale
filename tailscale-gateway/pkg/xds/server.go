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

package xds

import (
	"context"
	"fmt"
	"net"
	"time"

	extensionservice "github.com/envoyproxy/gateway/proto/extension"
	envoycluster "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	envoycore "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	envoyendpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	envoyroute "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	tailscalev1alpha1 "github.com/rajsinghtech/tailscale-gateway/api/v1alpha1"
)

var log = ctrl.Log.WithName("xds-server")

// Server implements the Envoy Gateway Extension Service
type Server struct {
	extensionservice.UnimplementedEnvoyGatewayExtensionServer
	client    client.Client
	scheme    *runtime.Scheme
	address   string
	namespace string
}

// NewServer creates a new xDS extension server
func NewServer(client client.Client, scheme *runtime.Scheme, address string, namespace string) *Server {
	return &Server{
		client:    client,
		scheme:    scheme,
		address:   address,
		namespace: namespace,
	}
}

// Start starts the gRPC server
func (s *Server) Start(ctx context.Context) error {
	lis, err := net.Listen("tcp", s.address)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	grpcServer := grpc.NewServer()
	extensionservice.RegisterEnvoyGatewayExtensionServer(grpcServer, s)

	log.Info("Starting xDS extension server", "address", s.address)

	go func() {
		<-ctx.Done()
		grpcServer.GracefulStop()
	}()

	return grpcServer.Serve(lis)
}

// PostRouteModify handles route modification hooks
func (s *Server) PostRouteModify(ctx context.Context, req *extensionservice.PostRouteModifyRequest) (*extensionservice.PostRouteModifyResponse, error) {
	log.V(2).Info("Processing PostRouteModify request")

	// Get TailscaleProxies for the current namespace
	var proxies tailscalev1alpha1.TailscaleProxyList
	if err := s.client.List(ctx, &proxies, client.InNamespace(s.namespace)); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list TailscaleProxies: %v", err))
	}

	// For now, we don't modify individual routes at this level
	// Egress routes are handled at the RouteConfiguration level
	return &extensionservice.PostRouteModifyResponse{
		Route: req.Route,
	}, nil
}

// PostVirtualHostModify handles virtual host modification hooks
func (s *Server) PostVirtualHostModify(ctx context.Context, req *extensionservice.PostVirtualHostModifyRequest) (*extensionservice.PostVirtualHostModifyResponse, error) {
	log.V(2).Info("Processing PostVirtualHostModify request")

	// Get TailscaleProxies for ingress configuration
	var proxies tailscalev1alpha1.TailscaleProxyList
	if err := s.client.List(ctx, &proxies, client.InNamespace(s.namespace)); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list TailscaleProxies: %v", err))
	}

	// Modify virtual hosts for ingress
	modifiedVHost := req.VirtualHost
	for _, proxy := range proxies.Items {
		if proxy.Spec.Type == tailscalev1alpha1.ProxyTypeIngress && proxy.Spec.IngressConfig != nil {
			// Add routes for ingress services
			for _, svc := range proxy.Spec.IngressConfig.Services {
				modifiedVHost = s.addIngressRoute(modifiedVHost, &proxy, &svc)
			}
		}
	}

	return &extensionservice.PostVirtualHostModifyResponse{
		VirtualHost: modifiedVHost,
	}, nil
}

// PostHTTPListenerModify handles HTTP listener modification hooks
func (s *Server) PostHTTPListenerModify(ctx context.Context, req *extensionservice.PostHTTPListenerModifyRequest) (*extensionservice.PostHTTPListenerModifyResponse, error) {
	log.V(2).Info("Processing PostHTTPListenerModify request")

	// Pass through without modification for now
	return &extensionservice.PostHTTPListenerModifyResponse{
		Listener: req.Listener,
	}, nil
}

// PostTranslateModify handles post-translation modification hooks
func (s *Server) PostTranslateModify(ctx context.Context, req *extensionservice.PostTranslateModifyRequest) (*extensionservice.PostTranslateModifyResponse, error) {
	log.V(2).Info("Processing PostTranslateModify request")

	// Get all TailscaleProxies
	var proxies tailscalev1alpha1.TailscaleProxyList
	if err := s.client.List(ctx, &proxies, client.InNamespace(s.namespace)); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list TailscaleProxies: %v", err))
	}

	// For now, we pass through clusters without modification
	// In the future, we can add Tailscale-specific cluster configurations here
	return &extensionservice.PostTranslateModifyResponse{
		Clusters: req.Clusters,
	}, nil
}

// addEgressRoute adds a route for a Tailscale egress service
func (s *Server) addEgressRoute(route *envoyroute.RouteConfiguration, proxy *tailscalev1alpha1.TailscaleProxy, svc *tailscalev1alpha1.EgressService) *envoyroute.RouteConfiguration {
	// Create a new route for the Tailscale service
	newRoute := &envoyroute.Route{
		Match: &envoyroute.RouteMatch{
			PathSpecifier: &envoyroute.RouteMatch_Prefix{
				Prefix: fmt.Sprintf("/%s", svc.Name),
			},
		},
		Action: &envoyroute.Route_Route{
			Route: &envoyroute.RouteAction{
				ClusterSpecifier: &envoyroute.RouteAction_Cluster{
					Cluster: fmt.Sprintf("tailscale-%s-%s", proxy.Name, svc.Name),
				},
			},
		},
	}

	// Add the route to the first virtual host
	if len(route.VirtualHosts) > 0 {
		route.VirtualHosts[0].Routes = append(route.VirtualHosts[0].Routes, newRoute)
	}

	return route
}

// addIngressRoute adds a route for a Tailscale ingress service
func (s *Server) addIngressRoute(vhost *envoyroute.VirtualHost, proxy *tailscalev1alpha1.TailscaleProxy, svc *tailscalev1alpha1.IngressService) *envoyroute.VirtualHost {
	// Create a new route for the ingress service
	path := svc.Path
	if path == "" {
		path = "/"
	}

	newRoute := &envoyroute.Route{
		Match: &envoyroute.RouteMatch{
			PathSpecifier: &envoyroute.RouteMatch_Prefix{
				Prefix: path,
			},
			Headers: []*envoyroute.HeaderMatcher{
				{
					Name: ":authority",
					HeaderMatchSpecifier: &envoyroute.HeaderMatcher_ExactMatch{
						ExactMatch: proxy.Spec.IngressConfig.Hostname,
					},
				},
			},
		},
		Action: &envoyroute.Route_Route{
			Route: &envoyroute.RouteAction{
				ClusterSpecifier: &envoyroute.RouteAction_Cluster{
					Cluster: fmt.Sprintf("service-%s-%d", svc.Name, svc.Port),
				},
			},
		},
	}

	vhost.Routes = append(vhost.Routes, newRoute)
	return vhost
}

// createTailscaleCluster creates an Envoy cluster for a Tailscale service
func (s *Server) createTailscaleCluster(proxy *tailscalev1alpha1.TailscaleProxy, svc *tailscalev1alpha1.EgressService) *envoycluster.Cluster {
	return &envoycluster.Cluster{
		Name:           fmt.Sprintf("tailscale-%s-%s", proxy.Name, svc.Name),
		ConnectTimeout: durationpb.New(30 * time.Second),
		ClusterDiscoveryType: &envoycluster.Cluster_Type{
			Type: envoycluster.Cluster_STATIC,
		},
		LoadAssignment: &envoyendpoint.ClusterLoadAssignment{
			ClusterName: fmt.Sprintf("tailscale-%s-%s", proxy.Name, svc.Name),
			Endpoints: []*envoyendpoint.LocalityLbEndpoints{
				{
					LbEndpoints: []*envoyendpoint.LbEndpoint{
						{
							HostIdentifier: &envoyendpoint.LbEndpoint_Endpoint{
								Endpoint: &envoyendpoint.Endpoint{
									Address: &envoycore.Address{
										Address: &envoycore.Address_SocketAddress{
											SocketAddress: &envoycore.SocketAddress{
												Protocol: envoycore.SocketAddress_TCP,
												Address:  fmt.Sprintf("%s-egress-svc", proxy.Name),
												PortSpecifier: &envoycore.SocketAddress_PortValue{
													PortValue: uint32(svc.Port),
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}
