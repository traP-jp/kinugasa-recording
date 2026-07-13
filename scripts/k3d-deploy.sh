#!/bin/sh
set -eu

public_host="${PUBLIC_HOST:-127.0.0.1}"

case "$public_host" in
	*[!A-Za-z0-9.:-]* | '')
		echo "PUBLIC_HOST contains unsupported characters: $public_host" >&2
		exit 2
		;;
esac

kustomize build config/overlays/k3d \
	| sed "s/127\.0\.0\.1/$public_host/g" \
	| kubectl apply -f -

kubectl -n kinugasa-recording rollout status deployment/redis --timeout=120s
kubectl -n kinugasa-recording rollout status deployment/livekit --timeout=120s
kubectl -n kinugasa-recording rollout status deployment/livekit-ingress-service --timeout=120s
kubectl -n kinugasa-recording rollout status deployment/operator --timeout=120s
kubectl -n kinugasa-recording rollout status deployment/web --timeout=120s

echo "Web UI: http://$public_host:30080"
