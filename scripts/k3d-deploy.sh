#!/bin/sh
set -eu

script_directory="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
public_host="$($script_directory/detect-public-ip.sh)"

kustomize build config/overlays/k3d \
	| sed "s/127\.0\.0\.1/$public_host/g" \
	| kubectl apply -f -

for deployment in redis livekit livekit-ingress-service operator web; do
	kubectl -n kinugasa-recording rollout restart "deployment/$deployment"
done

kubectl -n kinugasa-recording rollout status deployment/redis --timeout=120s
kubectl -n kinugasa-recording rollout status deployment/livekit --timeout=120s
kubectl -n kinugasa-recording rollout status deployment/livekit-ingress-service --timeout=120s
kubectl -n kinugasa-recording rollout status deployment/operator --timeout=120s
kubectl -n kinugasa-recording rollout status deployment/web --timeout=120s

echo "Web UI: http://$public_host:30080"
echo "LiveKit signaling: ws://$public_host:30081"
echo "LiveKit media: $public_host TCP/7881 and UDP/7882"
echo "Camera input NodePorts: $public_host UDP/31000-31099"
