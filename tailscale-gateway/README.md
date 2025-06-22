# Tailscale-Envoy Gateway Operator

A Kubernetes operator that integrates Tailscale with Envoy Gateway using the xDS extension server mechanism. This operator enables seamless connectivity between your Kubernetes cluster and Tailscale network by automatically configuring Envoy Gateway with Tailscale routes.

## Features

- **Bidirectional Connectivity**: Support for both ingress (exposing cluster services to Tailscale) and egress (accessing Tailscale services from cluster)
- **High Availability**: Built-in support for multiple replicas with proper coordination
- **xDS Integration**: Leverages Envoy Gateway's extension server for dynamic route configuration
- **Headless Services**: Uses headless services pattern for proper HA deployment
- **ConfigMap-based Configuration**: Flexible configuration management for proxy settings

## Architecture

The operator consists of two main components:

1. **Controller Manager**: Manages TailscaleProxy and TailscaleGateway custom resources
2. **xDS Extension Server**: Integrates with Envoy Gateway to inject Tailscale-specific routing configuration

### CRD Overview

#### TailscaleGateway
Manages the overall integration between Tailscale and Envoy Gateway:
- Deploys and manages the xDS extension server
- Configures authentication with Tailscale
- Sets up integration with Envoy Gateway

#### TailscaleProxy
Defines individual proxy configurations:
- **Ingress**: Exposes Kubernetes services to your Tailscale network
- **Egress**: Provides access to Tailscale services from within the cluster

## Prerequisites

- Kubernetes cluster (1.24+)
- Envoy Gateway installed and configured
- Tailscale account with auth key
- kubectl configured to access your cluster

## Installation

### 1. Install CRDs

```bash
kubectl apply -f https://raw.githubusercontent.com/rajsinghtech/tailscale-gateway/main/config/crd/bases/tailscale.rajsinghtech.com_tailscalegateways.yaml
kubectl apply -f https://raw.githubusercontent.com/rajsinghtech/tailscale-gateway/main/config/crd/bases/tailscale.rajsinghtech.com_tailscaleproxies.yaml
```

### 2. Create Tailscale Auth Key Secret

```bash
kubectl create namespace tailscale-gateway-system
kubectl create secret generic tailscale-auth-key \
  --from-literal=authkey=tskey-auth-YOUR-KEY-HERE \
  -n tailscale-gateway-system
```

### 3. Deploy the Operator

Using kustomize:
```bash
kubectl apply -k github.com/rajsinghtech/tailscale-gateway/config/default
```

Or using pre-built manifests:
```bash
kubectl apply -f https://raw.githubusercontent.com/rajsinghtech/tailscale-gateway/main/deploy/operator.yaml
```

## Usage

### 1. Create a TailscaleGateway

```yaml
apiVersion: tailscale.rajsinghtech.com/v1alpha1
kind: TailscaleGateway
metadata:
  name: main-gateway
spec:
  authKey:
    name: tailscale-auth-key
    namespace: tailscale-gateway-system
    key: authkey
  envoyGatewayRef:
    name: eg
    namespace: envoy-gateway-system
  gatewayClassName: tailscale-class
  xdsServerConfig:
    namespace: tailscale-gateway-system
    replicas: 2
  tailscaleConfig:
    acceptDNS: true
    acceptRoutes: true
    advertiseRoutes:
    - "10.0.0.0/8"
```

### 2. Create Ingress Proxy (Expose Services to Tailscale)

```yaml
apiVersion: tailscale.rajsinghtech.com/v1alpha1
kind: TailscaleProxy
metadata:
  name: web-ingress
  namespace: default
spec:
  type: ingress
  replicas: 2
  className: main-gateway
  ingressConfig:
    hostname: my-web-app
    services:
    - name: web-service
      protocol: https
      port: 443
      targetPort: 8080
      path: /
  tags:
  - tag:k8s-ingress
```

### 3. Create Egress Proxy (Access Tailscale Services)

```yaml
apiVersion: tailscale.rajsinghtech.com/v1alpha1
kind: TailscaleProxy
metadata:
  name: database-egress
  namespace: default
spec:
  type: egress
  replicas: 3
  className: main-gateway
  egressConfig:
    services:
    - name: postgres-db
      tailscaleTarget: postgres.tail1234.ts.net
      port: 5432
      protocol: tcp
  tags:
  - tag:k8s-egress
```

## Configuration

### TailscaleGateway Configuration

| Field | Description | Default |
|-------|-------------|---------|
| `authKey` | Reference to Tailscale auth key secret | Required |
| `envoyGatewayRef` | Reference to EnvoyGateway instance | Required |
| `gatewayClassName` | Name of the GatewayClass to manage | Required |
| `xdsServerConfig.replicas` | Number of xDS server replicas | 2 |
| `xdsServerConfig.image` | xDS server container image | rajsinghtech/tailscale-gateway:latest |
| `tailscaleConfig.acceptDNS` | Accept DNS configuration from tailnet | true |
| `tailscaleConfig.acceptRoutes` | Accept advertised routes | true |
| `tailscaleConfig.advertiseRoutes` | Routes to advertise to tailnet | [] |

### TailscaleProxy Configuration

#### Ingress Configuration
| Field | Description | Required |
|-------|-------------|----------|
| `hostname` | Tailscale hostname for the ingress | Yes |
| `services` | List of services to expose | Yes |
| `services[].protocol` | Protocol (http, https, tcp) | Yes |
| `services[].port` | Port to expose on Tailscale | Yes |
| `services[].targetPort` | Target port on the backend service | Yes |
| `services[].path` | Path prefix for HTTP/HTTPS services | No |
| `useFunnel` | Enable Tailscale Funnel for public access | No |

#### Egress Configuration
| Field | Description | Required |
|-------|-------------|----------|
| `services` | List of Tailscale services to access | Yes |
| `services[].name` | Service name to create in cluster | Yes |
| `services[].tailscaleTarget` | Tailscale hostname or IP | Yes |
| `services[].port` | Port on the Tailscale target | Yes |
| `services[].protocol` | Protocol (tcp, udp) | No (default: tcp) |

## Development

### Building from Source

```bash
# Clone the repository
git clone https://github.com/rajsinghtech/tailscale-gateway.git
cd tailscale-gateway

# Build the operator
make build

# Build container image
make docker-build IMG=your-registry/tailscale-gateway:latest

# Push to registry
make docker-push IMG=your-registry/tailscale-gateway:latest
```

### Running Locally

```bash
# Install CRDs
make install

# Run the operator locally
make run
```

### Running Tests

```bash
make test
```

## Troubleshooting

### Check Operator Logs
```bash
kubectl logs -n tailscale-gateway-system deployment/tailscale-gateway-controller-manager
```

### Check xDS Server Logs
```bash
kubectl logs -n tailscale-gateway-system deployment/xds-server
```

### Verify CRDs are Installed
```bash
kubectl get crd tailscalegateways.tailscale.rajsinghtech.com
kubectl get crd tailscaleproxies.tailscale.rajsinghtech.com
```

### Check Proxy Status
```bash
kubectl get tailscaleproxy -A
kubectl describe tailscaleproxy <proxy-name>
```

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

## Acknowledgments

- [Tailscale](https://tailscale.com/) for the amazing VPN solution
- [Envoy Gateway](https://gateway.envoyproxy.io/) for the extensible gateway implementation
- The Kubernetes community for the operator framework