// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

//go:build !plan9

package main

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	hcmv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	clustersvc "github.com/envoyproxy/go-control-plane/envoy/service/cluster/v3"
	discoveryv3 "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	endpointsvc "github.com/envoyproxy/go-control-plane/envoy/service/endpoint/v3"
	listenersvc "github.com/envoyproxy/go-control-plane/envoy/service/listener/v3"
	routesvc "github.com/envoyproxy/go-control-plane/envoy/service/route/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	cachev3 "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	serverv3 "github.com/envoyproxy/go-control-plane/pkg/server/v3"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	
	tsapi "tailscale.com/k8s-operator/apis/v1alpha1"
)

const (
	// xDS type URLs
	ListenerType = "type.googleapis.com/envoy.config.listener.v3.Listener"
	ClusterType  = "type.googleapis.com/envoy.config.cluster.v3.Cluster"
	RouteType    = "type.googleapis.com/envoy.config.route.v3.RouteConfiguration"
	EndpointType = "type.googleapis.com/envoy.config.endpoint.v3.ClusterLoadAssignment"
	
	// xDS server constants
	xdsGRPCPort = 18000
	xdsNodeHashKey = "proxygroup"
)

// XDSServer manages the xDS API server for Envoy configuration
type XDSServer struct {
	cache      cachev3.SnapshotCache
	server     serverv3.Server
	grpcServer *grpc.Server
	mu         sync.RWMutex
	snapshots  map[string]string // nodeID -> snapshotVersion
	l          *zap.SugaredLogger
}

// NewXDSServer creates a new xDS server instance
func NewXDSServer(logger *zap.SugaredLogger) *XDSServer {
	cache := cachev3.NewSnapshotCache(false, cachev3.IDHash{}, logger.Named("xds-cache"))
	
	return &XDSServer{
		cache:     cache,
		server:    serverv3.NewServer(context.Background(), cache, nil),
		snapshots: make(map[string]string),
		l:         logger.Named("xds-server"),
	}
}

// Start starts the xDS gRPC server
func (x *XDSServer) Start(ctx context.Context) error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", xdsGRPCPort))
	if err != nil {
		return fmt.Errorf("failed to listen on xDS port: %w", err)
	}
	
	x.grpcServer = grpc.NewServer(
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    30 * time.Second,
			Timeout: 5 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             30 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	
	// Register all xDS server types
	discoveryv3.RegisterAggregatedDiscoveryServiceServer(x.grpcServer, x.server)
	endpointsvc.RegisterEndpointDiscoveryServiceServer(x.grpcServer, x.server)
	clustersvc.RegisterClusterDiscoveryServiceServer(x.grpcServer, x.server)
	listenersvc.RegisterListenerDiscoveryServiceServer(x.grpcServer, x.server)
	routesvc.RegisterRouteDiscoveryServiceServer(x.grpcServer, x.server)
	
	go func() {
		<-ctx.Done()
		x.grpcServer.GracefulStop()
	}()
	
	x.l.Infof("Starting xDS server on port %d", xdsGRPCPort)
	return x.grpcServer.Serve(lis)
}

// UpdateConfiguration updates the xDS configuration for a ProxyGroup
func (x *XDSServer) UpdateConfiguration(ctx context.Context, pg *tsapi.ProxyGroup, services []ServiceRoute) error {
	nodeID := x.getNodeID(pg)
	version := fmt.Sprintf("%d", time.Now().Unix())
	
	// Generate xDS resources
	listeners, err := x.generateListeners(pg, services)
	if err != nil {
		return fmt.Errorf("failed to generate listeners: %w", err)
	}
	
	clusters, err := x.generateClusters(services)
	if err != nil {
		return fmt.Errorf("failed to generate clusters: %w", err)
	}
	
	routes, err := x.generateRoutes(services)
	if err != nil {
		return fmt.Errorf("failed to generate routes: %w", err)
	}
	
	endpoints, err := x.generateEndpoints(services)
	if err != nil {
		return fmt.Errorf("failed to generate endpoints: %w", err)
	}
	
	// Create snapshot
	snapshot, err := cachev3.NewSnapshot(
		version,
		map[resource.Type][]types.Resource{
			resource.EndpointType: endpoints,
			resource.ClusterType:  clusters,
			resource.RouteType:    routes,
			resource.ListenerType: listeners,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to create xDS snapshot: %w", err)
	}
	
	// Set snapshot in cache
	if err := x.cache.SetSnapshot(ctx, nodeID, snapshot); err != nil {
		return fmt.Errorf("failed to set xDS snapshot: %w", err)
	}
	
	x.mu.Lock()
	x.snapshots[nodeID] = version
	x.mu.Unlock()
	
	x.l.Infof("Updated xDS configuration for ProxyGroup %s (version: %s)", pg.Name, version)
	return nil
}

// getNodeID generates a consistent node ID for a ProxyGroup
func (x *XDSServer) getNodeID(pg *tsapi.ProxyGroup) string {
	return fmt.Sprintf("%s.%s", pg.Name, pg.Namespace)
}

// generateListeners creates Envoy listener configurations
func (x *XDSServer) generateListeners(pg *tsapi.ProxyGroup, services []ServiceRoute) ([]types.Resource, error) {
	var listeners []types.Resource
	
	// HTTP listener
	httpConnectionManager := &hcmv3.HttpConnectionManager{
		StatPrefix: "ingress_http",
		RouteSpecifier: &hcmv3.HttpConnectionManager_Rds{
			Rds: &hcmv3.Rds{
				ConfigSource: &corev3.ConfigSource{
					ResourceApiVersion: corev3.ApiVersion_V3,
					ConfigSourceSpecifier: &corev3.ConfigSource_ApiConfigSource{
						ApiConfigSource: &corev3.ApiConfigSource{
							ApiType:                   corev3.ApiConfigSource_GRPC,
							TransportApiVersion:       corev3.ApiVersion_V3,
							SetNodeOnFirstMessageOnly: true,
							GrpcServices: []*corev3.GrpcService{
								{
									TargetSpecifier: &corev3.GrpcService_EnvoyGrpc_{
										EnvoyGrpc: &corev3.GrpcService_EnvoyGrpc{
											ClusterName: "xds_cluster",
										},
									},
								},
							},
						},
					},
				},
				RouteConfigName: "main_route",
			},
		},
		HttpFilters: []*hcmv3.HttpFilter{
			{
				Name: wellknown.Router,
				ConfigType: &hcmv3.HttpFilter_TypedConfig{
					TypedConfig: &anypb.Any{
						TypeUrl: "type.googleapis.com/envoy.extensions.filters.http.router.v3.Router",
					},
				},
			},
		},
	}
	
	hcmAny, err := anypb.New(httpConnectionManager)
	if err != nil {
		return nil, err
	}
	
	listener := &listenerv3.Listener{
		Name: "http_listener",
		Address: &corev3.Address{
			Address: &corev3.Address_SocketAddress{
				SocketAddress: &corev3.SocketAddress{
					Protocol: corev3.SocketAddress_TCP,
					Address:  "0.0.0.0",
					PortSpecifier: &corev3.SocketAddress_PortValue{
						PortValue: envoyHTTPPort,
					},
				},
			},
		},
		FilterChains: []*listenerv3.FilterChain{
			{
				Filters: []*listenerv3.Filter{
					{
						Name: wellknown.HTTPConnectionManager,
						ConfigType: &listenerv3.Filter_TypedConfig{
							TypedConfig: hcmAny,
						},
					},
				},
			},
		},
	}
	
	listeners = append(listeners, listener)
	return listeners, nil
}

// generateClusters creates Envoy cluster configurations
func (x *XDSServer) generateClusters(services []ServiceRoute) ([]types.Resource, error) {
	var clusters []types.Resource
	
	for _, svc := range services {
		cluster := &clusterv3.Cluster{
			Name:                 fmt.Sprintf("%s_%s_cluster", svc.Name, svc.Namespace),
			ConnectTimeout:       durationpb.New(5 * time.Second),
			ClusterDiscoveryType: &clusterv3.Cluster_Type{Type: clusterv3.Cluster_EDS},
			EdsClusterConfig: &clusterv3.Cluster_EdsClusterConfig{
				EdsConfig: &corev3.ConfigSource{
					ResourceApiVersion: corev3.ApiVersion_V3,
					ConfigSourceSpecifier: &corev3.ConfigSource_ApiConfigSource{
						ApiConfigSource: &corev3.ApiConfigSource{
							ApiType:                   corev3.ApiConfigSource_GRPC,
							TransportApiVersion:       corev3.ApiVersion_V3,
							SetNodeOnFirstMessageOnly: true,
							GrpcServices: []*corev3.GrpcService{
								{
									TargetSpecifier: &corev3.GrpcService_EnvoyGrpc_{
										EnvoyGrpc: &corev3.GrpcService_EnvoyGrpc{
											ClusterName: "xds_cluster",
										},
									},
								},
							},
						},
					},
				},
			},
			LbPolicy: clusterv3.Cluster_ROUND_ROBIN,
		}
		clusters = append(clusters, cluster)
	}
	
	return clusters, nil
}

// generateRoutes creates Envoy route configurations
func (x *XDSServer) generateRoutes(services []ServiceRoute) ([]types.Resource, error) {
	var routes []*routev3.Route
	
	// Sort services to ensure specific routes come before generic ones
	sortedServices := make([]ServiceRoute, len(services))
	copy(sortedServices, services)
	// Sorting logic already implemented in proxygroup_envoy.go
	
	for _, svc := range sortedServices {
		route := &routev3.Route{
			Match: &routev3.RouteMatch{
				PathSpecifier: &routev3.RouteMatch_Prefix{
					Prefix: svc.PathPrefix,
				},
			},
			Action: &routev3.Route_Route{
				Route: &routev3.RouteAction{
					ClusterSpecifier: &routev3.RouteAction_Cluster{
						Cluster: fmt.Sprintf("%s_%s_cluster", svc.Name, svc.Namespace),
					},
				},
			},
		}
		
		// Add host header matching if specified
		if svc.HostHeader != "" {
			route.Match.Headers = []*routev3.HeaderMatcher{
				{
					Name: ":authority",
					HeaderMatchSpecifier: &routev3.HeaderMatcher_ExactMatch{
						ExactMatch: svc.HostHeader,
					},
				},
			}
		}
		
		routes = append(routes, route)
	}
	
	routeConfig := &routev3.RouteConfiguration{
		Name: "main_route",
		VirtualHosts: []*routev3.VirtualHost{
			{
				Name:    "backend",
				Domains: []string{"*"},
				Routes:  routes,
			},
		},
	}
	
	return []types.Resource{routeConfig}, nil
}

// generateEndpoints creates Envoy endpoint configurations
func (x *XDSServer) generateEndpoints(services []ServiceRoute) ([]types.Resource, error) {
	var endpoints []types.Resource
	
	for _, svc := range services {
		endpoint := &endpointv3.ClusterLoadAssignment{
			ClusterName: fmt.Sprintf("%s_%s_cluster", svc.Name, svc.Namespace),
			Endpoints: []*endpointv3.LocalityLbEndpoints{
				{
					LbEndpoints: []*endpointv3.LbEndpoint{
						{
							HostIdentifier: &endpointv3.LbEndpoint_Endpoint{
								Endpoint: &endpointv3.Endpoint{
									Address: &corev3.Address{
										Address: &corev3.Address_SocketAddress{
											SocketAddress: &corev3.SocketAddress{
												Protocol: corev3.SocketAddress_TCP,
												Address:  svc.ClusterIP,
												PortSpecifier: &corev3.SocketAddress_PortValue{
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
		}
		endpoints = append(endpoints, endpoint)
	}
	
	return endpoints, nil
}

// RemoveConfiguration removes xDS configuration for a ProxyGroup
func (x *XDSServer) RemoveConfiguration(pg *tsapi.ProxyGroup) {
	nodeID := x.getNodeID(pg)
	x.cache.ClearSnapshot(nodeID)
	
	x.mu.Lock()
	delete(x.snapshots, nodeID)
	x.mu.Unlock()
	
	x.l.Infof("Removed xDS configuration for ProxyGroup %s", pg.Name)
}