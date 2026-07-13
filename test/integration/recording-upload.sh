#!/bin/sh
set -eu

namespace="${KINUGASA_NAMESPACE:-kinugasa-recording}"
cluster="${K3D_CLUSTER_NAME:-kinugasa-recording}"
node="k3d-$cluster-server-0"
session="recording-integration"
session_name="Recording-Integration"
take="take-1"
camera="front"
selector="recording.kinugasa.tra.pt/session=$session"
node_ip="$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$node")"
s3_endpoint="http://$node_ip:19000"
sender_pod="recording-sender"
original_endpoint=""
original_path_style="false"
mock_started="false"

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

wait_for_object() {
	for _ in $(seq 1 90); do
		objects="$(curl --fail --silent "$s3_endpoint/_objects" 2>/dev/null || true)"
		object_path="$(printf '%s' "$objects" | sed -n 's#.*\(kinugasa-recording/Recording-Integration/take-1/front/segment-[0-9][0-9]*\.ts\).*#\1#p')"
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

kubectl -n "$namespace" patch configmap kinugasa-recording-s3 --type=merge \
	--patch "{\"data\":{\"S3_ENDPOINT\":\"$s3_endpoint\",\"S3_USE_PATH_STYLE\":\"true\"}}" >/dev/null
import_image video-fanout
import_image livekit-ingress
kubectl -n "$namespace" delete krsession "$session" --ignore-not-found --wait=true >/dev/null
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
take_digest="$(printf '%s\0%s\0%s' "$session" "$take" "$camera" | sha256sum | cut -c1-24)"
take_base="take-$take_digest"
wait_for_resource "job/$take_base-recorder"
wait_for_resource "job/$take_base-uploader"
kubectl -n "$namespace" wait pod -l "job-name=$take_base-recorder" --for=condition=Ready --timeout=120s >/dev/null
kubectl -n "$namespace" wait pod -l "job-name=$take_base-uploader" --for=condition=Ready --timeout=120s >/dev/null
wait_for_value "krsession/$session" '{.status.takes[0].phase}' Recording
wait_for_object

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

kubectl -n "$namespace" patch "krsession/$session" --type=merge --patch \
	'{"spec":{"cameras":[{"name":"front","desiredState":"Absent","ingress":{"ristNodePort":31000,"srtNodePort":31001}}]}}' >/dev/null
wait_for_value "krsession/$session" '{.status.cameras[0].phase}' Removed

echo "MPEG-TS recording and incremental S3 upload integration test passed"
