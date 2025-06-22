#!/bin/bash

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
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

log_step() {
    echo -e "${BLUE}[STEP]${NC} $1"
}

# Check if kubectl is available and cluster is accessible
check_cluster() {
    log_info "Checking cluster access..."
    
    if ! command -v kubectl &> /dev/null; then
        log_error "kubectl is not installed or not in PATH"
        exit 1
    fi
    
    if ! kubectl cluster-info &> /dev/null; then
        log_error "Cannot access Kubernetes cluster. Please ensure kubectl is configured."
        exit 1
    fi
    
    log_info "Cluster access confirmed"
}

# Load Docker image into kind cluster (if using kind)
load_image() {
    log_step "Loading Docker image into cluster..."
    
    # Check if we're using kind
    if kubectl config current-context | grep -q "kind"; then
        CLUSTER_NAME=$(kubectl config current-context | sed 's/kind-//')
        log_info "Detected kind cluster: $CLUSTER_NAME"
        log_info "Loading tailscale-gateway:latest image..."
        
        if kind load docker-image tailscale-gateway:latest --name "$CLUSTER_NAME"; then
            log_info "Image loaded successfully"
        else
            log_warn "Failed to load image, continuing anyway..."
        fi
    else
        log_info "Not a kind cluster, assuming image is available in cluster registry"
    fi
}

# Deploy the operator
deploy_operator() {
    log_step "Deploying Tailscale Gateway Operator..."
    
    # Create namespace
    log_info "Creating namespace..."
    kubectl create namespace tailscale-gateway-system --dry-run=client -o yaml | kubectl apply -f -
    
    # Install CRDs
    log_info "Installing CRDs..."
    kubectl apply -f config/crd/bases/
    
    # Create RBAC
    log_info "Creating RBAC..."
    kubectl apply -f config/rbac/
    
    # Deploy manager
    log_info "Deploying controller manager..."
    kubectl apply -f config/manager/manager.yaml
    
    # Wait for deployment to be ready
    log_info "Waiting for controller manager to be ready..."
    kubectl wait --for=condition=available deployment/tailscale-gateway-controller-manager \
        -n tailscale-gateway-system --timeout=300s
    
    log_info "Operator deployed successfully"
}

# Create Tailscale OAuth secret
create_secret() {
    log_step "Creating Tailscale OAuth secret..."
    
    kubectl apply -f test/tailscale-secret.yaml
    
    log_info "OAuth secret created"
}

# Deploy test application
deploy_test_app() {
    log_step "Deploying test application..."
    
    kubectl apply -f test/test-app.yaml
    
    # Wait for deployment
    log_info "Waiting for test app to be ready..."
    kubectl wait --for=condition=available deployment/test-app -n default --timeout=300s
    
    log_info "Test application deployed successfully"
}

# Create TailscaleGateway
create_gateway() {
    log_step "Creating TailscaleGateway..."
    
    kubectl apply -f test/test-gateway.yaml
    
    # Wait a bit for the controller to process
    sleep 10
    
    # Check status
    log_info "Gateway status:"
    kubectl get tailscalegateways -n tailscale-gateway-system
    kubectl describe tailscalegateways test-gateway -n tailscale-gateway-system
}

# Create TailscaleProxy resources
create_proxies() {
    log_step "Creating TailscaleProxy resources..."
    
    # Create ingress proxy
    log_info "Creating ingress proxy..."
    kubectl apply -f test/test-ingress-proxy.yaml
    
    # Create egress proxy
    log_info "Creating egress proxy..."
    kubectl apply -f test/test-egress-proxy.yaml
    
    # Wait a bit for processing
    sleep 15
    
    # Check status
    log_info "Proxy status:"
    kubectl get tailscaleproxies -n tailscale-gateway-system
    
    log_info "Ingress proxy details:"
    kubectl describe tailscaleproxies test-ingress -n tailscale-gateway-system
    
    log_info "Egress proxy details:"
    kubectl describe tailscaleproxies test-egress -n tailscale-gateway-system
}

# Show cluster resources
show_resources() {
    log_step "Showing created resources..."
    
    log_info "All resources in tailscale-gateway-system namespace:"
    kubectl get all -n tailscale-gateway-system
    
    log_info "ConfigMaps:"
    kubectl get configmaps -n tailscale-gateway-system
    
    log_info "Secrets:"
    kubectl get secrets -n tailscale-gateway-system
    
    log_info "Custom resources:"
    kubectl get tailscalegateways,tailscaleproxies -A
}

# Show logs
show_logs() {
    log_step "Showing recent logs..."
    
    log_info "Controller manager logs (last 50 lines):"
    kubectl logs -n tailscale-gateway-system deployment/tailscale-gateway-controller-manager --tail=50
    
    # Check if xDS server deployment exists
    if kubectl get deployment test-gateway-xds-server -n tailscale-gateway-system &> /dev/null; then
        log_info "xDS Server logs (last 20 lines):"
        kubectl logs -n tailscale-gateway-system deployment/test-gateway-xds-server --tail=20
    fi
    
    # Check if proxy StatefulSets exist
    if kubectl get statefulset test-ingress -n tailscale-gateway-system &> /dev/null; then
        log_info "Ingress proxy logs (last 20 lines):"
        kubectl logs -n tailscale-gateway-system statefulset/test-ingress --tail=20
    fi
    
    if kubectl get statefulset test-egress -n tailscale-gateway-system &> /dev/null; then
        log_info "Egress proxy logs (last 20 lines):"
        kubectl logs -n tailscale-gateway-system statefulset/test-egress --tail=20
    fi
}

# Cleanup function
cleanup() {
    log_step "Cleaning up test resources..."
    
    log_info "Removing TailscaleProxy resources..."
    kubectl delete -f test/test-ingress-proxy.yaml --ignore-not-found=true
    kubectl delete -f test/test-egress-proxy.yaml --ignore-not-found=true
    
    log_info "Removing TailscaleGateway..."
    kubectl delete -f test/test-gateway.yaml --ignore-not-found=true
    
    log_info "Removing test application..."
    kubectl delete -f test/test-app.yaml --ignore-not-found=true
    
    log_info "Removing OAuth secret..."
    kubectl delete -f test/tailscale-secret.yaml --ignore-not-found=true
    
    if [[ "$1" == "--full" ]]; then
        log_info "Removing operator..."
        kubectl delete -f config/manager/manager.yaml --ignore-not-found=true
        kubectl delete -f config/rbac/ --ignore-not-found=true
        kubectl delete -f config/crd/bases/ --ignore-not-found=true
        kubectl delete namespace tailscale-gateway-system --ignore-not-found=true
    fi
    
    log_info "Cleanup completed"
}

# Main execution
main() {
    case "${1:-deploy}" in
        deploy)
            log_info "Starting Tailscale Gateway Operator deployment and testing..."
            check_cluster
            load_image
            deploy_operator
            create_secret
            deploy_test_app
            create_gateway
            create_proxies
            show_resources
            show_logs
            log_info "Deployment completed! Check the logs above for any issues."
            log_info "Use './test/deploy-and-test.sh logs' to see logs again."
            log_info "Use './test/deploy-and-test.sh cleanup' to remove test resources."
            ;;
        logs)
            log_info "Showing current logs..."
            show_logs
            ;;
        status)
            log_info "Showing current status..."
            show_resources
            ;;
        cleanup)
            cleanup
            ;;
        cleanup-all)
            cleanup --full
            ;;
        *)
            echo "Usage: $0 [deploy|logs|status|cleanup|cleanup-all]"
            echo ""
            echo "Commands:"
            echo "  deploy      - Deploy operator and test resources (default)"
            echo "  logs        - Show recent logs from all components"
            echo "  status      - Show current resource status"
            echo "  cleanup     - Remove test resources (keep operator)"
            echo "  cleanup-all - Remove everything including operator"
            exit 1
            ;;
    esac
}

# Run main function with all arguments
main "$@"