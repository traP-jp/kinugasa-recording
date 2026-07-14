#!/bin/sh
set -eu

namespace="${KINUGASA_NAMESPACE:-kinugasa-recording}"
cluster="${K3D_CLUSTER_NAME:-kinugasa-recording}"
node="k3d-$cluster-server-0"
session_name="${E2E_SESSION_NAME:-E2E-Basic-$(date +%s)-$$}"
camera_front="front"
camera_side="side"
take_all="take-all"
take_selected="take-front"
script_directory="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH= cd -- "$script_directory/../.." && pwd)"
public_host="$("$repository_root/scripts/detect-public-ip.sh")"
web_url="http://$public_host:30080"
node_ip="$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$node")"
s3_endpoint="http://$node_ip:19000"
sender_pids=""
mock_started="false"
original_endpoint=""
original_path_style="false"
original_config_captured="false"
curl_flags="--fail-with-body --silent --show-error"
object_file="/tmp/kinugasa-e2e-object-$$"

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
	stop_senders
	rm -f "$object_file"
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
		for sender_pid in $sender_pids; do
			if ! kill -0 "$sender_pid" >/dev/null 2>&1; then
				echo "host media sender stopped while waiting for API $filter=$want" >&2
				return 1
			fi
		done
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

start_sender() {
	srt_url="$1"
	for _ in $(seq 1 30); do
		ffmpeg -hide_banner -loglevel error -re -f lavfi -i testsrc=size=320x180:rate=15 \
			-an -c:v libx264 -preset ultrafast -tune zerolatency -profile:v baseline -pix_fmt yuv420p \
			-x264-params repeat-headers=1:keyint=30 -f mpegts "$srt_url" >/dev/null 2>&1 &
		sender_pid=$!
		sleep 2
		if kill -0 "$sender_pid" >/dev/null 2>&1; then
			sender_pids="$sender_pids $sender_pid"
			return 0
		fi
		wait "$sender_pid" 2>/dev/null || true
	done
	echo "media sender could not connect to $srt_url" >&2
	return 1
}

stop_senders() {
	for sender_pid in $sender_pids; do
		kill "$sender_pid" >/dev/null 2>&1 || true
		wait "$sender_pid" 2>/dev/null || true
	done
	sender_pids=""
}

wait_for_object() {
	take_name="$1"
	camera_name="$2"
	prefix="kinugasa-recording/$session_name/$take_name/$camera_name/segment-"
	for _ in $(seq 1 90); do
		for sender_pid in $sender_pids; do
			if ! kill -0 "$sender_pid" >/dev/null 2>&1; then
				echo "host media sender stopped while waiting for object $prefix*.ts" >&2
				return 1
			fi
		done
		objects="$(curl $curl_flags "$s3_endpoint/_objects")"
		object_path="$(printf '%s' "$objects" | jq -r --arg prefix "$prefix" '.[] | select(startswith($prefix) and endswith(".ts"))' | head -n 1)"
		[ -z "$object_path" ] || return 0
		sleep 1
	done
	echo "timed out waiting for object $prefix*.ts" >&2
	kubectl -n "$namespace" get jobs,pods,pvc >&2 || true
	for pod in $(kubectl -n "$namespace" get pods -o name | sed -n '/take-/p'); do
		kubectl -n "$namespace" logs "$pod" >&2 || true
	done
	return 1
}

start_camera_workloads() {
	resource_name="$1"
	for _ in $(seq 1 120); do
		deployments="$(kubectl -n "$namespace" get deployment -l "recording.kinugasa.tra.pt/session=$resource_name" -o name 2>/dev/null || true)"
		[ "$(printf '%s\n' "$deployments" | sed '/^$/d' | wc -l | tr -d ' ')" = 4 ] && break
		sleep 1
	done
	import_image livekit-ingress
	for deployment in $(printf '%s\n' "$deployments" | sed -n '/-ingress$/p'); do
		kubectl -n "$namespace" wait "$deployment" --for=condition=Available --timeout=120s >/dev/null
	done
	import_image video-fanout
	for deployment in $(printf '%s\n' "$deployments" | sed -n '/-fanout$/p'); do
		kubectl -n "$namespace" wait "$deployment" --for=condition=Available --timeout=120s >/dev/null
	done
}

start_take_workloads() {
	take_name="$1"
	shift
	bases=""
	for camera_name in "$@"; do
		digest="$(printf '%s\0%s\0%s' "$resource_name" "$take_name" "$camera_name" | sha256sum | cut -c1-24)"
		base="take-$digest"
		bases="$bases $base"
		for _ in $(seq 1 120); do
			kubectl -n "$namespace" get "job/$base-uploader" >/dev/null 2>&1 && kubectl -n "$namespace" get "job/$base-recorder" >/dev/null 2>&1 && break
			sleep 1
		done
	done
	import_image video-uploader
	for base in $bases; do
		kubectl -n "$namespace" wait pod -l "job-name=$base-uploader" --for=condition=Ready --timeout=120s >/dev/null
	done
	import_image video-recorder
	for base in $bases; do
		kubectl -n "$namespace" wait pod -l "job-name=$base-recorder" --for=condition=Ready --timeout=120s >/dev/null
	done
}

expect_name_reserved() {
	method="$1"
	path="$2"
	idempotency_key="$3"
	data="$4"
	response="$(curl --silent --show-error --request "$method" "$web_url$path" \
		--header 'Content-Type: application/json' --header "Idempotency-Key: $idempotency_key" \
		--data "$data" --write-out '\n%{http_code}')"
	status="$(printf '%s\n' "$response" | tail -n 1)"
	body="$(printf '%s\n' "$response" | sed '$d')"
	test "$status" = 409
	test "$(printf '%s' "$body" | jq -r '.error.code')" = NAME_RESERVED
}

verify_camera_objects() {
	take_index="$1"
	take_name="$2"
	camera_name="$3"
	session_body="$(curl $curl_flags "$web_url/api/v1/sessions/$session_name")"
	expected="$(printf '%s' "$session_body" | jq -r --arg camera "$camera_name" ".session.status.takes[$take_index].cameras[] | select(.name == \$camera) | .uploadedFiles")"
	objects="$(curl $curl_flags "$s3_endpoint/_objects")"
	prefix="kinugasa-recording/$session_name/$take_name/$camera_name/segment-"
	paths="$(printf '%s' "$objects" | jq -r --arg prefix "$prefix" '.[] | select(startswith($prefix) and endswith(".ts"))')"
	actual="$(printf '%s\n' "$paths" | sed '/^$/d' | wc -l | tr -d ' ')"
	if [ -z "$expected" ] || [ "$expected" -le 0 ] || [ "$actual" != "$expected" ]; then
		echo "uploaded object count mismatch for $take_name/$camera_name: status=$expected S3=$actual" >&2
		return 1
	fi
	for path in $paths; do
		content_type="$(curl $curl_flags --head "$s3_endpoint/$path" | tr -d '\r' | sed -n 's/^Content-Type: //Ip')"
		curl $curl_flags --output "$object_file" "$s3_endpoint/$path"
		first_byte="$(od -An -tu1 -N1 "$object_file" | tr -d ' ')"
		if [ "$content_type" != video/mp2t ] || [ "$first_byte" != 71 ]; then
			echo "invalid recording object $path: content-type=$content_type first-byte=$first_byte" >&2
			return 1
		fi
	done
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
expect_name_reserved POST /api/v1/sessions e2e-create-session-duplicate "{\"name\":\"$session_name\"}"

added_front="$(curl $curl_flags --request POST "$web_url/api/v1/sessions/$session_name/cameras" \
	--header 'Content-Type: application/json' --header 'Idempotency-Key: e2e-add-front' \
	--data "{\"name\":\"$camera_front\"}")"
added_side="$(curl $curl_flags --request POST "$web_url/api/v1/sessions/$session_name/cameras" \
	--header 'Content-Type: application/json' --header 'Idempotency-Key: e2e-add-side' \
	--data "{\"name\":\"$camera_side\"}")"
front_srt_url="$(printf '%s' "$added_front" | jq -r '.connectionUrls.srt')"
side_srt_url="$(printf '%s' "$added_side" | jq -r '.connectionUrls.srt')"
test "$front_srt_url" = "srt://$public_host:31001?mode=caller&transtype=live"
test "$side_srt_url" = "srt://$public_host:31003?mode=caller&transtype=live"
expect_name_reserved POST "/api/v1/sessions/$session_name/cameras" e2e-add-front-duplicate "{\"name\":\"$camera_front\"}"
wait_for_api_value '.session.status.cameras[0].phase' Waiting
wait_for_api_value '.session.status.cameras[1].phase' Waiting
resource_name="$(session_resource)"
start_camera_workloads "$resource_name"

start_sender "$front_srt_url"
start_sender "$side_srt_url"
wait_for_api_value '.session.status.cameras[0].phase' Connected
wait_for_api_value '.session.status.cameras[0].connectedProtocol' srt
wait_for_api_value '.session.status.cameras[1].phase' Connected
wait_for_api_value '.session.status.cameras[1].connectedProtocol' srt

token="$(curl $curl_flags --request POST "$web_url/api/v1/livekit/token" --header 'Content-Type: application/json' --data '{}')"
test "$(printf '%s' "$token" | jq -r '.serverUrl')" = "ws://$public_host:30081"
test -n "$(printf '%s' "$token" | jq -r '.participantToken')"

started="$(curl $curl_flags --request POST "$web_url/api/v1/sessions/$session_name/takes" \
	--header 'Content-Type: application/json' --header 'Idempotency-Key: e2e-start-all' \
	--data "{\"name\":\"$take_all\",\"cameraNames\":[]}")"
test "$(printf '%s' "$started" | jq -r '.take.cameraNames | length')" = 2
test "$(printf '%s' "$started" | jq -r --arg camera "$camera_front" '.take.cameraNames | index($camera) != null')" = true
test "$(printf '%s' "$started" | jq -r --arg camera "$camera_side" '.take.cameraNames | index($camera) != null')" = true
start_take_workloads "$take_all" "$camera_front" "$camera_side"
wait_for_api_value '.session.status.takes[0].phase' Recording

wait_for_object "$take_all" "$camera_front"
wait_for_object "$take_all" "$camera_side"

curl $curl_flags --output /dev/null --request POST "$web_url/api/v1/sessions/$session_name/takes/$take_all/stop" \
	--header 'Content-Type: application/json' --header 'Idempotency-Key: e2e-stop-all' --data '{}'
wait_for_api_value '.session.status.takes[0].phase' Completed
verify_camera_objects 0 "$take_all" "$camera_front"
verify_camera_objects 0 "$take_all" "$camera_side"
expect_name_reserved POST "/api/v1/sessions/$session_name/takes" e2e-start-all-duplicate "{\"name\":\"$take_all\",\"cameraNames\":[]}"

selected="$(curl $curl_flags --request POST "$web_url/api/v1/sessions/$session_name/takes" \
	--header 'Content-Type: application/json' --header 'Idempotency-Key: e2e-start-front' \
	--data "{\"name\":\"$take_selected\",\"cameraNames\":[\"$camera_front\"]}")"
test "$(printf '%s' "$selected" | jq -r '.take.cameraNames | length')" = 1
test "$(printf '%s' "$selected" | jq -r '.take.cameraNames[0]')" = "$camera_front"
start_take_workloads "$take_selected" "$camera_front"
wait_for_api_value '.session.status.takes[1].phase' Recording
wait_for_object "$take_selected" "$camera_front"
objects="$(curl $curl_flags "$s3_endpoint/_objects")"
test "$(printf '%s' "$objects" | jq -r --arg prefix "kinugasa-recording/$session_name/$take_selected/$camera_side/" '[.[] | select(startswith($prefix))] | length')" = 0
curl $curl_flags --output /dev/null --request POST "$web_url/api/v1/sessions/$session_name/takes/$take_selected/stop" \
	--header 'Content-Type: application/json' --header 'Idempotency-Key: e2e-stop-front' --data '{}'
wait_for_api_value '.session.status.takes[1].phase' Completed
verify_camera_objects 1 "$take_selected" "$camera_front"

stop_senders
curl $curl_flags --output /dev/null --request DELETE "$web_url/api/v1/sessions/$session_name/cameras/$camera_front" \
	--header 'Idempotency-Key: e2e-delete-front'
curl $curl_flags --output /dev/null --request DELETE "$web_url/api/v1/sessions/$session_name/cameras/$camera_side" \
	--header 'Idempotency-Key: e2e-delete-side'
wait_for_api_value '.session.status.cameras[0].phase' Removed
wait_for_api_value '.session.status.cameras[1].phase' Removed

echo "Basic multi-camera UC-006/UC-001/UC-003/UC-002/UC-004 end-to-end flow passed"
