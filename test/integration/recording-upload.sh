#!/bin/sh
set -eu

namespace="${KINUGASA_NAMESPACE:-kinugasa-recording}"
cluster="${K3D_CLUSTER_NAME:-kinugasa-recording}"
node="k3d-$cluster-server-0"
session="recording-integration"
session_name="Recording-Integration"
take="take-1"
failed_take="take-failure"
camera="front"
selector="recording.kinugasa.tra.pt/session=$session"
node_ip="$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$node")"
s3_endpoint="http://$node_ip:19000"
sender_pod="recording-sender"
original_endpoint=""
original_path_style="false"
mock_started="false"
take_digest="$(printf '%s\0%s\0%s' "$session" "$take" "$camera" | sha256sum | cut -c1-24)"
take_base="take-$take_digest"
failed_digest="$(printf '%s\0%s\0%s' "$session" "$failed_take" "$camera" | sha256sum | cut -c1-24)"
failed_base="take-$failed_digest"

import_image() {
	k3d image import --cluster "$cluster" "kinugasa-recording/$1:latest" >/dev/null
}

wait_for_value() {
	resource="$1"
	jsonpath="$2"
	want="$3"
	for _ in $(seq 1 180); do
		value="$(kubectl -n "$namespace" get "$resource" -o "jsonpath=$jsonpath" 2>/dev/null || true)"
		if [ "$value" = "$want" ]; then
			return 0
		fi
		sleep 1
	done
	echo "timed out waiting for $resource $jsonpath=$want (last value: $value)" >&2
	return 1
}

wait_for_count() {
	resource="$1"
	want="$2"
	for _ in $(seq 1 120); do
		count="$(kubectl -n "$namespace" get "$resource" -l "$selector" -o name 2>/dev/null | wc -l | tr -d ' ')"
		if [ "$count" = "$want" ]; then
			return 0
		fi
		sleep 1
	done
	echo "timed out waiting for $want $resource resources (last count: $count)" >&2
	return 1
}

wait_for_resource() {
	resource="$1"
	for _ in $(seq 1 120); do
		if kubectl -n "$namespace" get "$resource" >/dev/null 2>&1; then
			return 0
		fi
		sleep 1
	done
	echo "timed out waiting for $resource" >&2
	return 1
}

wait_for_absent() {
	resource="$1"
	for _ in $(seq 1 120); do
		if ! kubectl -n "$namespace" get "$resource" >/dev/null 2>&1; then
			return 0
		fi
		sleep 1
	done
	echo "timed out waiting for $resource deletion" >&2
	return 1
}

wait_for_object() {
	take_name="$1"
	for _ in $(seq 1 90); do
		objects="$(curl --fail --silent "$s3_endpoint/_objects" 2>/dev/null || true)"
		object_path="$(printf '%s' "$objects" | sed -n "s#.*\(kinugasa-recording/Recording-Integration/$take_name/front/segment-[0-9][0-9]*\.ts\).*#\1#p")"
		if [ -n "$object_path" ]; then
			return 0
		fi
		sleep 1
	done
	echo "timed out waiting for an uploaded recording object (objects: $objects)" >&2
	return 1
}

cleanup() {
	kubectl -n "$namespace" delete krsession "$session" --ignore-not-found --wait=false >/dev/null 2>&1 || true
	kubectl -n "$namespace" delete pod "$sender_pod" --ignore-not-found --wait=false >/dev/null 2>&1 || true
	if [ "$mock_started" = true ]; then
		docker exec "$node" sh -c 'kill $(pidof kinugasa-s3mock) 2>/dev/null || true' >/dev/null 2>&1 || true
		docker exec "$node" rm -f /tmp/kinugasa-s3mock >/dev/null 2>&1 || true
	fi
	kubectl -n "$namespace" patch configmap kinugasa-recording-s3 --type=merge \
		--patch "{\"data\":{\"S3_ENDPOINT\":\"$original_endpoint\",\"S3_USE_PATH_STYLE\":\"$original_path_style\"}}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

original_endpoint="$(kubectl -n "$namespace" get configmap kinugasa-recording-s3 -o 'jsonpath={.data.S3_ENDPOINT}')"
original_path_style="$(kubectl -n "$namespace" get configmap kinugasa-recording-s3 -o 'jsonpath={.data.S3_USE_PATH_STYLE}')"

docker exec "$node" sh -c 'kill $(pidof kinugasa-s3mock) 2>/dev/null || true' >/dev/null 2>&1 || true
CGO_ENABLED=0 go build -o /tmp/kinugasa-s3mock ./test/integration/s3mock
docker cp /tmp/kinugasa-s3mock "$node:/tmp/kinugasa-s3mock"
docker exec -d "$node" /tmp/kinugasa-s3mock
mock_started=true
for _ in $(seq 1 30); do
	if curl --fail --silent --output /dev/null "$s3_endpoint/_health"; then
		break
	fi
	sleep 1
done
curl --fail --silent --output /dev/null "$s3_endpoint/_health"
curl --fail --silent --request POST \
	"$s3_endpoint/_control?put_failures=100&put_status=503&put_code=SlowDown" --output /dev/null

kubectl -n "$namespace" patch configmap kinugasa-recording-s3 --type=merge \
	--patch "{\"data\":{\"S3_ENDPOINT\":\"$s3_endpoint\",\"S3_USE_PATH_STYLE\":\"true\"}}" >/dev/null
import_image video-fanout
import_image livekit-ingress
kubectl -n "$namespace" delete krsession "$session" --ignore-not-found --wait=true >/dev/null
for base in "$take_base" "$failed_base"; do
	kubectl -n "$namespace" delete pod -l "job-name=$base-recorder" --ignore-not-found --wait=false >/dev/null
	kubectl -n "$namespace" delete pod -l "job-name=$base-uploader" --ignore-not-found --wait=false >/dev/null
	for resource in "job/$base-recorder" "job/$base-uploader" "service/$base-uploader" "pvc/$base"; do
		wait_for_absent "$resource"
	done
done
kubectl apply -f test/integration/recording-upload.yaml >/dev/null
wait_for_count deployment 2
kubectl -n "$namespace" wait deployment -l "$selector" --for=condition=Available --timeout=120s >/dev/null

input_service="$(kubectl -n "$namespace" get service -l "$selector" -o 'jsonpath={.items[?(@.spec.type=="NodePort")].metadata.name}')"
kubectl -n "$namespace" run "$sender_pod" --image=kinugasa-recording/video-fanout:latest --image-pull-policy=IfNotPresent --restart=Never --command -- \
	ffmpeg -hide_banner -loglevel error -re -f lavfi -i testsrc=size=320x180:rate=15 \
	-an -c:v libx264 -preset ultrafast -tune zerolatency -profile:v baseline -pix_fmt yuv420p \
	-x264-params repeat-headers=1:keyint=30 -f mpegts \
	"srt://$input_service:10001?mode=caller&transtype=live" >/dev/null
wait_for_value "krsession/$session" '{.status.cameras[0].connectedProtocol}' srt

requested_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
import_image video-recorder
import_image video-uploader
kubectl -n "$namespace" patch "krsession/$session" --type=merge --patch \
	"{\"spec\":{\"reservedTakeNames\":[\"$take\"],\"takes\":[{\"name\":\"$take\",\"desiredState\":\"Recording\",\"cameraNames\":[\"$camera\"],\"requestedAt\":\"$requested_at\"}]}}" >/dev/null
wait_for_resource "job/$take_base-recorder"
wait_for_resource "job/$take_base-uploader"
kubectl -n "$namespace" wait pod -l "job-name=$take_base-recorder" --for=condition=Ready --timeout=120s >/dev/null
kubectl -n "$namespace" wait pod -l "job-name=$take_base-uploader" --for=condition=Ready --timeout=120s >/dev/null
wait_for_value "krsession/$session" '{.status.takes[0].phase}' Recording
wait_for_value "krsession/$session" '{.status.takes[0].cameras[0].conditions[?(@.type=="UploadHealthy")].reason}' Retrying
temporary_failures="$(curl --fail --silent "$s3_endpoint/_stats" | sed -n 's/.*"failuresServed":\([0-9][0-9]*\).*/\1/p')"
test "$temporary_failures" -gt 0
curl --fail --silent --request POST \
	"$s3_endpoint/_control?put_failures=0" --output /dev/null
wait_for_object "$take"
wait_for_value "krsession/$session" '{.status.takes[0].cameras[0].conditions[?(@.type=="UploadHealthy")].reason}' Uploading

content_type="$(curl --fail --silent --head "$s3_endpoint/$object_path" | tr -d '\r' | sed -n 's/^Content-Type: //Ip')"
first_byte="$(curl --fail --silent "$s3_endpoint/$object_path" | od -An -tu1 -N1 | tr -d ' ')"
test "$content_type" = video/mp2t
test "$first_byte" = 71
if [ "$(kubectl -n "$namespace" get "krsession/$session" -o 'jsonpath={.spec.takes[0].desiredState}')" != Recording ]; then
	echo "recording object was not uploaded while the take was active" >&2
	exit 1
fi

stopped_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
kubectl -n "$namespace" patch "krsession/$session" --type=merge --patch \
	"{\"spec\":{\"takes\":[{\"name\":\"$take\",\"desiredState\":\"Stopped\",\"cameraNames\":[\"$camera\"],\"requestedAt\":\"$requested_at\",\"stopRequestedAt\":\"$stopped_at\"}]}}" >/dev/null
wait_for_value "krsession/$session" '{.status.takes[0].phase}' Completed

failed_requested_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
curl --fail --silent --request POST \
	"$s3_endpoint/_control?put_failures=100&put_status=403&put_code=AccessDenied" --output /dev/null
import_image video-recorder
import_image video-uploader
kubectl -n "$namespace" patch "krsession/$session" --type=merge --patch \
	"{\"spec\":{\"reservedTakeNames\":[\"$take\",\"$failed_take\"],\"takes\":[{\"name\":\"$take\",\"desiredState\":\"Stopped\",\"cameraNames\":[\"$camera\"],\"requestedAt\":\"$requested_at\",\"stopRequestedAt\":\"$stopped_at\"},{\"name\":\"$failed_take\",\"desiredState\":\"Recording\",\"cameraNames\":[\"$camera\"],\"requestedAt\":\"$failed_requested_at\"}]}}" >/dev/null
wait_for_resource "job/$failed_base-recorder"
wait_for_resource "job/$failed_base-uploader"
kubectl -n "$namespace" wait pod -l "job-name=$failed_base-recorder" --for=condition=Ready --timeout=120s >/dev/null
kubectl -n "$namespace" wait pod -l "job-name=$failed_base-uploader" --for=condition=Ready --timeout=120s >/dev/null
wait_for_value "krsession/$session" '{.status.takes[1].phase}' Recording
wait_for_value "krsession/$session" '{.status.takes[1].cameras[0].uploadPhase}' Failed
wait_for_value "krsession/$session" '{.status.takes[1].cameras[0].conditions[?(@.type=="UploadHealthy")].reason}' PermanentFailure
failed_stopped_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
kubectl -n "$namespace" patch "krsession/$session" --type=merge --patch \
	"{\"spec\":{\"takes\":[{\"name\":\"$take\",\"desiredState\":\"Stopped\",\"cameraNames\":[\"$camera\"],\"requestedAt\":\"$requested_at\",\"stopRequestedAt\":\"$stopped_at\"},{\"name\":\"$failed_take\",\"desiredState\":\"Stopped\",\"cameraNames\":[\"$camera\"],\"requestedAt\":\"$failed_requested_at\",\"stopRequestedAt\":\"$failed_stopped_at\"}]}}" >/dev/null
wait_for_value "krsession/$session" '{.status.takes[1].phase}' Uploading

kubectl -n "$namespace" patch "krsession/$session" --type=merge --patch \
	'{"spec":{"cameras":[{"name":"front","desiredState":"Absent","ingress":{"ristNodePort":31000,"srtNodePort":31001}}]}}' >/dev/null
wait_for_value "krsession/$session" '{.status.cameras[0].phase}' Removed

echo "MPEG-TS recording and incremental S3 upload integration test passed"
