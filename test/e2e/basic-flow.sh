#!/bin/sh
set -eu

namespace="${KINUGASA_NAMESPACE:-kinugasa-recording}"
cluster="${K3D_CLUSTER_NAME:-kinugasa-recording}"
node="k3d-$cluster-server-0"
session_name="E2E-Basic"
camera="front"
take="take-1"
script_directory="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH= cd -- "$script_directory/../.." && pwd)"
public_host="$("$repository_root/scripts/detect-public-ip.sh")"
web_url="http://$public_host:30080"
node_ip="$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$node")"
s3_endpoint="http://$node_ip:19000"
sender_pid=""
mock_started="false"
original_endpoint=""
original_path_style="false"
original_config_captured="false"
curl_flags="--fail-with-body --silent --show-error"

import_image() {
	k3d image import --cluster "$cluster" "kinugasa-recording/$1:latest" >/dev/null
}

session_resource() {
	kubectl -n "$namespace" get krsession -o json | jq -r --arg name "$session_name" '.items[] | select(.spec.name == $name) | .metadata.name' | head -n 1
}

restart_operator() {
	kubectl -n "$namespace" rollout restart deployment/operator >/dev/null
	kubectl -n "$namespace" rollout status deployment/operator --timeout=120s >/dev/null
	for _ in $(seq 1 30); do
		if curl $curl_flags --output /dev/null "$web_url/api/v1/sessions" 2>/dev/null; then
			return 0
		fi
		sleep 1
	done
	echo "operator API did not become reachable through Web" >&2
	return 1
}

cleanup() {
	if [ -n "$sender_pid" ]; then
		kill "$sender_pid" >/dev/null 2>&1 || true
		wait "$sender_pid" 2>/dev/null || true
	fi
	resource="$(session_resource 2>/dev/null || true)"
	if [ -n "$resource" ]; then
		kubectl -n "$namespace" delete "krsession/$resource" --wait=false >/dev/null 2>&1 || true
	fi
	if [ "$mock_started" = true ]; then
		docker exec "$node" sh -c 'kill $(pidof kinugasa-s3mock) 2>/dev/null || true' >/dev/null 2>&1 || true
		docker exec "$node" rm -f /tmp/kinugasa-s3mock >/dev/null 2>&1 || true
	fi
	if [ "$original_config_captured" = true ]; then
		kubectl -n "$namespace" patch configmap kinugasa-recording-s3 --type=merge \
			--patch "{\"data\":{\"S3_ENDPOINT\":\"$original_endpoint\",\"S3_USE_PATH_STYLE\":\"$original_path_style\"}}" >/dev/null 2>&1 || true
		restart_operator >/dev/null 2>&1 || true
	fi
}
trap cleanup EXIT

wait_for_api_value() {
	filter="$1"
	want="$2"
	for _ in $(seq 1 180); do
		if [ -n "$sender_pid" ] && ! kill -0 "$sender_pid" >/dev/null 2>&1; then
			echo "host media sender stopped while waiting for API $filter=$want" >&2
			return 1
		fi
		body="$(curl $curl_flags "$web_url/api/v1/sessions/$session_name" 2>/dev/null || true)"
		value="$(printf '%s' "$body" | jq -r "$filter // empty" 2>/dev/null || true)"
		if [ "$value" = "$want" ]; then
			return 0
		fi
		sleep 1
	done
	echo "timed out waiting for API $filter=$want (last value: $value)" >&2
	return 1
}

original_endpoint="$(kubectl -n "$namespace" get configmap kinugasa-recording-s3 -o 'jsonpath={.data.S3_ENDPOINT}')"
original_path_style="$(kubectl -n "$namespace" get configmap kinugasa-recording-s3 -o 'jsonpath={.data.S3_USE_PATH_STYLE}')"
original_config_captured=true

docker exec "$node" sh -c 'kill $(pidof kinugasa-s3mock) 2>/dev/null || true' >/dev/null 2>&1 || true
CGO_ENABLED=0 go build -o /tmp/kinugasa-s3mock ./test/integration/s3mock
docker cp /tmp/kinugasa-s3mock "$node:/tmp/kinugasa-s3mock"
docker exec -d "$node" /tmp/kinugasa-s3mock
mock_started=true
for _ in $(seq 1 30); do
	if curl $curl_flags --output /dev/null "$s3_endpoint/_health"; then
		break
	fi
	sleep 1
done
curl $curl_flags --output /dev/null "$s3_endpoint/_health"
kubectl -n "$namespace" patch configmap kinugasa-recording-s3 --type=merge \
	--patch "{\"data\":{\"S3_ENDPOINT\":\"$s3_endpoint\",\"S3_USE_PATH_STYLE\":\"true\"}}" >/dev/null
restart_operator

existing_resource="$(session_resource)"
if [ -n "$existing_resource" ]; then
	kubectl -n "$namespace" delete "krsession/$existing_resource" --wait=true >/dev/null
fi

created="$(curl $curl_flags --request POST "$web_url/api/v1/sessions" \
	--header 'Content-Type: application/json' --header 'Idempotency-Key: e2e-create-session' \
	--data "{\"name\":\"$session_name\"}")"
test "$(printf '%s' "$created" | jq -r '.session.name')" = "$session_name"

import_image video-fanout
import_image livekit-ingress
added="$(curl $curl_flags --request POST "$web_url/api/v1/sessions/$session_name/cameras" \
	--header 'Content-Type: application/json' --header 'Idempotency-Key: e2e-add-camera' \
	--data "{\"name\":\"$camera\"}")"
srt_url="$(printf '%s' "$added" | jq -r '.connectionUrls.srt')"
test "$srt_url" = "srt://$public_host:31001?mode=caller&transtype=live"
wait_for_api_value '.session.status.cameras[0].phase' Waiting

ffmpeg -hide_banner -loglevel error -re -f lavfi -i testsrc=size=320x180:rate=15 \
	-an -c:v libx264 -preset ultrafast -tune zerolatency -profile:v baseline -pix_fmt yuv420p \
	-x264-params repeat-headers=1:keyint=30 -f mpegts "$srt_url" >/dev/null 2>&1 &
sender_pid=$!
wait_for_api_value '.session.status.cameras[0].phase' Connected
wait_for_api_value '.session.status.cameras[0].connectedProtocol' srt

token="$(curl $curl_flags --request POST "$web_url/api/v1/livekit/token" --header 'Content-Type: application/json' --data '{}')"
test "$(printf '%s' "$token" | jq -r '.serverUrl')" = "ws://$public_host:30081"
test -n "$(printf '%s' "$token" | jq -r '.participantToken')"

import_image video-recorder
import_image video-uploader
started="$(curl $curl_flags --request POST "$web_url/api/v1/sessions/$session_name/takes" \
	--header 'Content-Type: application/json' --header 'Idempotency-Key: e2e-start-take' \
	--data "{\"name\":\"$take\",\"cameraNames\":[]}")"
test "$(printf '%s' "$started" | jq -r '.take.cameraNames[0]')" = "$camera"
wait_for_api_value '.session.status.takes[0].phase' Recording

for _ in $(seq 1 90); do
	objects="$(curl $curl_flags "$s3_endpoint/_objects")"
	object_path="$(printf '%s' "$objects" | jq -r '.[] | select(test("E2E-Basic/take-1/front/segment-[0-9]+\\.ts$"))' | head -n 1)"
	[ -z "$object_path" ] || break
	sleep 1
done
test -n "$object_path"

curl $curl_flags --output /dev/null --request POST "$web_url/api/v1/sessions/$session_name/takes/$take/stop" \
	--header 'Content-Type: application/json' --header 'Idempotency-Key: e2e-stop-take' --data '{}'
wait_for_api_value '.session.status.takes[0].phase' Completed
test "$(curl $curl_flags --head "$s3_endpoint/$object_path" | tr -d '\r' | sed -n 's/^Content-Type: //Ip')" = video/mp2t

curl $curl_flags --output /dev/null --request DELETE "$web_url/api/v1/sessions/$session_name/cameras/$camera" \
	--header 'Idempotency-Key: e2e-delete-camera'
wait_for_api_value '.session.status.cameras[0].phase' Removed

echo "Basic UC-006/UC-001/UC-003/UC-002/UC-004 end-to-end flow passed"
