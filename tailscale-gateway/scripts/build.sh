#!/bin/bash
set -e

# Default values
IMAGE_REGISTRY=${IMAGE_REGISTRY:-"rajsinghtech"}
IMAGE_NAME=${IMAGE_NAME:-"tailscale-gateway"}
VERSION=${VERSION:-"latest"}

# Build flags
CGO_ENABLED=${CGO_ENABLED:-0}
GOOS=${GOOS:-linux}
GOARCH=${GOARCH:-amd64}

echo "Building Tailscale Gateway Operator..."
echo "Image: ${IMAGE_REGISTRY}/${IMAGE_NAME}:${VERSION}"

# Build the binary
echo "Building Go binary..."
CGO_ENABLED=${CGO_ENABLED} GOOS=${GOOS} GOARCH=${GOARCH} go build -a -o bin/manager cmd/main.go

# Build the Docker image
echo "Building Docker image..."
docker build -t ${IMAGE_REGISTRY}/${IMAGE_NAME}:${VERSION} .

# Push if requested
if [ "$1" == "push" ]; then
    echo "Pushing Docker image..."
    docker push ${IMAGE_REGISTRY}/${IMAGE_NAME}:${VERSION}
fi

echo "Build complete!"