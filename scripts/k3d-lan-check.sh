#!/bin/sh
set -eu

namespace="${KINUGASA_NAMESPACE:-kinugasa-recording}"
script_directory="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
public_host="$($script_directory/detect-public-ip.sh)"

curl --fail --silent --show-error --output /dev/null "http://$public_host:30080/"
curl --fail --silent --show-error --output /dev/null "http://$public_host:30081/"

configured_media_host="$(kubectl -n "$namespace" get configmap kinugasa-recording-operator -o 'jsonpath={.data.PUBLIC_MEDIA_HOST}')"
configured_livekit_url="$(kubectl -n "$namespace" get configmap kinugasa-recording-operator -o 'jsonpath={.data.LIVEKIT_PUBLIC_URL}')"
test "$configured_media_host" = "$public_host"
test "$configured_livekit_url" = "ws://$public_host:30081"

echo "LAN endpoint configuration is ready for device verification:"
echo "  Web UI: http://$public_host:30080"
echo "  LiveKit: ws://$public_host:30081, TCP/7881, UDP/7882"
echo "  Camera input: UDP/31000-31099"
