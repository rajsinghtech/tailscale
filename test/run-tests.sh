#!/bin/bash

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Logging functions
log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check if required tools are available
check_tools() {
    log_info "Checking required tools..."
    
    for tool in kind docker kubectl; do
        if ! command -v $tool &> /dev/null; then
            log_error "$tool is not installed or not in PATH"
            exit 1
        fi
    done
    
    log_info "All required tools are available"
}

# Create kind cluster
create_cluster() {
    log_info "Creating kind cluster..."
    
    if kind get clusters | grep -q "tailscale-gateway-test"; then
        log_warn "Cluster already exists, deleting..."
        kind delete cluster --name tailscale-gateway-test
    fi
    
    kind create cluster --config test/kind-config.yaml
    
    log_info "Waiting for cluster to be ready..."
    kubectl wait --for=condition=Ready nodes --all --timeout=300s
}

# Build operator image
build_image() {
    log_info "Building operator image..."
    
    # Build the operator
    make docker-build IMG=tailscale-gateway:latest
    
    # Load image into kind cluster
    kind load docker-image tailscale-gateway:latest --name tailscale-gateway-test
    
    log_info "Operator image built and loaded into cluster"
}

# Install Envoy Gateway
install_envoy_gateway() {
    log_info "Installing Envoy Gateway..."
    
    # Install Envoy Gateway
    kubectl apply -f https://github.com/envoyproxy/gateway/releases/download/v1.2.0/install.yaml
    
    # Wait for Envoy Gateway to be ready
    kubectl wait --timeout=300s --for=condition=Available deployment/envoy-gateway -n envoy-gateway-system
    
    log_info "Envoy Gateway installed successfully"
}

# Deploy operator
deploy_operator() {
    log_info "Deploying Tailscale Gateway operator..."
    
    # Create namespace
    kubectl create namespace tailscale-gateway-system --dry-run=client -o yaml | kubectl apply -f -
    
    # Apply CRDs and operator
    make deploy IMG=tailscale-gateway:latest
    
    # Wait for operator to be ready
    kubectl wait --timeout=300s --for=condition=Available deployment/tailscale-gateway-controller-manager -n tailscale-gateway-system
    
    log_info "Operator deployed successfully"
}

# Deploy test resources
deploy_test_resources() {
    log_info "Deploying test resources..."
    
    # Deploy Tailscale OAuth secret
    kubectl apply -f test/tailscale-secret.yaml
    
    # Deploy test application
    kubectl apply -f test/test-app.yaml
    
    # Wait for test app to be ready
    kubectl wait --timeout=300s --for=condition=Available deployment/test-app -n default
    
    # Deploy TailscaleGateway
    kubectl apply -f test/test-gateway.yaml
    
    # Deploy TailscaleProxy resources
    kubectl apply -f test/test-ingress-proxy.yaml
    kubectl apply -f test/test-egress-proxy.yaml
    
    log_info "Test resources deployed"
}

# Monitor resources
monitor_resources() {
    log_info "Monitoring resource status..."
    
    echo ""
    echo "=== Cluster Nodes ==="
    kubectl get nodes -o wide
    
    echo ""
    echo "=== Tailscale Gateway System ==="
    kubectl get all -n tailscale-gateway-system
    
    echo ""
    echo "=== TailscaleGateway ==="
    kubectl get tailscalegateways -n tailscale-gateway-system -o wide
    
    echo ""
    echo "=== TailscaleProxy ==="
    kubectl get tailscaleproxies -n tailscale-gateway-system -o wide
    
    echo ""
    echo "=== Test Application ==="
    kubectl get all -n default
    
    echo ""
    echo "=== Envoy Gateway ==="
    kubectl get all -n envoy-gateway-system
}

# Check logs
check_logs() {
    log_info "Checking operator logs..."
    
    echo ""
    echo "=== Operator Logs (last 50 lines) ==="
    kubectl logs -n tailscale-gateway-system deployment/tailscale-gateway-controller-manager --tail=50 || true
    
    echo ""
    echo "=== Ingress Proxy Logs ==="
    kubectl logs -n tailscale-gateway-system -l app.kubernetes.io/name=test-ingress --tail=20 || true
    
    echo ""
    echo "=== Egress Proxy Logs ==="
    kubectl logs -n tailscale-gateway-system -l app.kubernetes.io/name=test-egress --tail=20 || true
}

# Run connectivity tests
run_tests() {
    log_info "Running connectivity tests..."
    
    # Test 1: Check if ingress proxy is running
    log_info "Test 1: Checking ingress proxy status..."
    if kubectl get pods -n tailscale-gateway-system -l app.kubernetes.io/name=test-ingress | grep -q Running; then
        log_info "✓ Ingress proxy is running"
    else
        log_warn "✗ Ingress proxy is not running"
    fi
    
    # Test 2: Check if egress proxy is running
    log_info "Test 2: Checking egress proxy status..."
    if kubectl get pods -n tailscale-gateway-system -l app.kubernetes.io/name=test-egress | grep -q Running; then
        log_info "✓ Egress proxy is running"
    else
        log_warn "✗ Egress proxy is not running"
    fi
    
    # Test 3: Check if xDS server is running
    log_info "Test 3: Checking xDS server status..."
    if kubectl get pods -n tailscale-gateway-system -l app.kubernetes.io/name=test-gateway | grep -q Running; then
        log_info "✓ xDS server is running"
    else
        log_warn "✗ xDS server is not running"
    fi
    
    # Test 4: Check if test app is accessible
    log_info "Test 4: Testing application connectivity..."
    if kubectl run test-curl --image=curlimages/curl --rm -i --restart=Never -- curl -s http://test-app.default.svc.cluster.local | grep -q "Welcome to nginx"; then
        log_info "✓ Test application is accessible"
    else
        log_warn "✗ Test application is not accessible"
    fi
}

# Cleanup function
cleanup() {
    log_info "Cleaning up..."
    kind delete cluster --name tailscale-gateway-test || true
}

# Main execution
main() {
    log_info "Starting Tailscale Gateway operator tests..."
    
    # Set trap for cleanup on exit
    trap cleanup EXIT
    
    check_tools
    create_cluster
    build_image
    install_envoy_gateway
    deploy_operator
    deploy_test_resources
    
    # Wait a bit for everything to settle
    log_info "Waiting for resources to stabilize..."
    sleep 30
    
    monitor_resources
    check_logs
    run_tests
    
    log_info "Test run completed! Check the output above for any issues."
    log_info "To keep the cluster running for manual testing, press Ctrl+C now."
    log_info "Otherwise, the cluster will be cleaned up in 60 seconds..."
    
    sleep 60
}

# Run main function
main "$@"