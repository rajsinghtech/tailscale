# Quick Start Guide - Tailscale Gateway Operator Testing

## Prerequisites
- Kubernetes cluster (kind, k3s, minikube, or cloud)
- kubectl configured
- Docker (for building)

## 🚀 One-Command Deploy

```bash
# Build, deploy, and test everything
make docker-build IMG=tailscale-gateway:latest && ./test/deploy-and-test.sh
```

## 📋 Step-by-Step

### 1. Build the Operator
```bash
make docker-build IMG=tailscale-gateway:latest
```

### 2. Load Image (kind clusters only)
```bash
kind load docker-image tailscale-gateway:latest --name <cluster-name>
```

### 3. Deploy and Test
```bash
./test/deploy-and-test.sh
```

## 🔍 Monitor Progress

### Check Status
```bash
./test/deploy-and-test.sh status
```

### View Logs
```bash
./test/deploy-and-test.sh logs
```

### Manual Monitoring
```bash
# Watch all resources
kubectl get tailscalegateways,tailscaleproxies,pods,deployments,statefulsets -A -w

# Check operator logs
kubectl logs -n tailscale-gateway-system deployment/tailscale-gateway-controller-manager -f
```

## 🧹 Cleanup

### Remove Test Resources
```bash
./test/deploy-and-test.sh cleanup
```

### Remove Everything
```bash
./test/deploy-and-test.sh cleanup-all
```

## ✅ Success Indicators

1. **Operator Running**: Controller manager pod in `Running` state
2. **CRDs Installed**: `kubectl get crds | grep tailscale` shows resources
3. **Devices Registered**: Check Tailscale admin console for new devices
4. **Network Connectivity**: Test app accessible via Tailscale

## 🔧 Configured OAuth Credentials

- **Client ID**: `kNVPxShcJr11CNTRL`
- **Client Secret**: `tskey-client-kNVPxShcJr11CNTRL-dWwE9qS9yueY3xYSpNymuemeDJ4hZteL`

## 📖 Full Documentation

- **Complete Test Results**: `test/TEST_RESULTS.md`
- **Detailed Summary**: `TESTING_SUMMARY.md`
- **Project README**: `README.md`

---

**Ready to test!** 🎯 Run the one-command deploy above to get started.