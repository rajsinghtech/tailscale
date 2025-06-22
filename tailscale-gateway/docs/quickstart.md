# Quick Start Guide

This guide will help you get the Tailscale-Envoy Gateway operator running in your cluster in under 10 minutes.

## Prerequisites

Before you begin, ensure you have:

1. A Kubernetes cluster (version 1.24+)
2. `kubectl` configured to access your cluster
3. Envoy Gateway installed ([installation guide](https://gateway.envoyproxy.io/docs/install/))
4. A Tailscale account and auth key ([sign up here](https://login.tailscale.com/start))

## Step 1: Install the Operator

Deploy the Tailscale-Envoy Gateway operator:

```bash
kubectl apply -f https://raw.githubusercontent.com/rajsinghtech/tailscale-gateway/main/deploy/operator.yaml
```

Verify the installation:

```bash
kubectl get pods -n tailscale-gateway-system
```

You should see the controller manager pod running.

## Step 2: Create Tailscale Auth Key Secret

Create a secret with your Tailscale auth key:

```bash
kubectl create secret generic tailscale-auth-key \
  --from-literal=authkey=tskey-auth-XXXXXXXXX-XXXXXXXXXXXXXXXXXXXXXX \
  -n tailscale-gateway-system
```

> **Note**: Replace the auth key with your actual Tailscale auth key. You can generate one from the [Tailscale admin console](https://login.tailscale.com/admin/settings/keys).

## Step 3: Create a TailscaleGateway

Create a file named `tailscale-gateway.yaml`:

```yaml
apiVersion: tailscale.rajsinghtech.com/v1alpha1
kind: TailscaleGateway
metadata:
  name: my-tailscale-gateway
spec:
  authKey:
    name: tailscale-auth-key
    namespace: tailscale-gateway-system
    key: authkey
  envoyGatewayRef:
    name: eg
    namespace: envoy-gateway-system
  gatewayClassName: tailscale
```

Apply it:

```bash
kubectl apply -f tailscale-gateway.yaml
```

## Step 4: Expose a Service to Tailscale (Ingress)

Let's expose a service running in your cluster to your Tailscale network.

First, create a sample application:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: hello-world
  namespace: default
spec:
  selector:
    app: hello-world
  ports:
  - port: 80
    targetPort: 8080
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: hello-world
  namespace: default
spec:
  replicas: 1
  selector:
    matchLabels:
      app: hello-world
  template:
    metadata:
      labels:
        app: hello-world
    spec:
      containers:
      - name: hello-world
        image: gcr.io/google-samples/hello-app:1.0
        ports:
        - containerPort: 8080
```

Now create a TailscaleProxy to expose it:

```yaml
apiVersion: tailscale.rajsinghtech.com/v1alpha1
kind: TailscaleProxy
metadata:
  name: hello-world-ingress
  namespace: default
spec:
  type: ingress
  className: my-tailscale-gateway
  ingressConfig:
    hostname: hello-world
    services:
    - name: hello-world
      protocol: http
      port: 80
      targetPort: 80
  tags:
  - tag:k8s
```

Apply both:

```bash
kubectl apply -f hello-world-app.yaml
kubectl apply -f hello-world-ingress.yaml
```

After a few moments, you should be able to access your service at `http://hello-world:80` from any device on your Tailscale network!

## Step 5: Access a Tailscale Service from Cluster (Egress)

Let's say you have a database running on another machine in your Tailscale network. You can make it accessible to your cluster applications:

```yaml
apiVersion: tailscale.rajsinghtech.com/v1alpha1
kind: TailscaleProxy
metadata:
  name: database-egress
  namespace: default
spec:
  type: egress
  className: my-tailscale-gateway
  egressConfig:
    services:
    - name: production-db
      tailscaleTarget: database.tail1234.ts.net
      port: 5432
      protocol: tcp
  tags:
  - tag:k8s
```

Apply it:

```bash
kubectl apply -f database-egress.yaml
```

Now your cluster applications can connect to the database using `production-db.default.svc.cluster.local:5432`!

## Verification

Check the status of your resources:

```bash
# Check TailscaleGateway status
kubectl get tailscalegateway

# Check TailscaleProxy status
kubectl get tailscaleproxy -A

# View detailed information
kubectl describe tailscaleproxy hello-world-ingress
```

## Troubleshooting

If something isn't working:

1. Check operator logs:
   ```bash
   kubectl logs -n tailscale-gateway-system deployment/tailscale-gateway-controller-manager
   ```

2. Check proxy pod logs:
   ```bash
   kubectl logs -n default -l app=tailscale-proxy
   ```

3. Verify your Tailscale auth key is valid and hasn't expired

4. Ensure your ACLs allow the tagged devices to communicate

## Next Steps

- Read the [full documentation](../README.md) for advanced configuration options
- Learn about [high availability setup](./ha-setup.md)
- Configure [Tailscale Funnel](./funnel-setup.md) for public access
- Set up [monitoring and observability](./monitoring.md)

## Common Issues

### Auth Key Expired
If you see authentication errors, your auth key may have expired. Generate a new one and update the secret:

```bash
kubectl delete secret tailscale-auth-key -n tailscale-gateway-system
kubectl create secret generic tailscale-auth-key \
  --from-literal=authkey=tskey-auth-NEW-KEY \
  -n tailscale-gateway-system
```

### Service Not Accessible
Ensure your Tailscale ACLs allow access between the tagged devices. The default tags used are `tag:k8s`, `tag:k8s-ingress`, and `tag:k8s-egress`.

### Pods Not Starting
Check if the pods have sufficient resources and that the Tailscale container image can be pulled:

```bash
kubectl describe pod -n default -l app=tailscale-proxy
```