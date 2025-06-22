#!/bin/bash
set -e

# Check if kubectl is available
if ! command -v kubectl &> /dev/null; then
    echo "kubectl is not installed. Please install kubectl first."
    exit 1
fi

# Check if kustomize is available
if ! command -v kustomize &> /dev/null; then
    echo "kustomize is not installed. Installing to ./bin/kustomize..."
    make kustomize
fi

KUSTOMIZE=${KUSTOMIZE:-"./bin/kustomize"}

echo "Deploying Tailscale Gateway Operator..."

# Deploy using kustomize
echo "Applying manifests..."
${KUSTOMIZE} build config/default | kubectl apply -f -

echo "Deployment complete!"
echo ""
echo "Next steps:"
echo "1. Create a Tailscale auth key secret:"
echo "   kubectl create secret generic tailscale-auth-key \\"
echo "     --from-literal=authkey=tskey-auth-YOUR-KEY \\"
echo "     -n tailscale-gateway-system"
echo ""
echo "2. Create a TailscaleGateway resource (see config/samples/)"
echo "3. Create TailscaleProxy resources for ingress/egress"