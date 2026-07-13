#!/bin/sh
set -eu

namespace="${KINUGASA_NAMESPACE:-kinugasa-recording}"
session="session-integration"
selector="recording.kinugasa.tra.pt/session=$session"
sender_pod=""

stop_sender() {
	if [ -n "$sender_pod" ]; then
		kubectl -n "$namespace" delete pod "$sender_pod" --ignore-not-found --wait=false >/dev/null
		sender_pod=""
	fi
}

cleanup() {
	stop_sender
	kubectl -n "$namespace" delete pod recording-probe-rist recording-probe-srt --ignore-not-found --wait=false >/dev/null
	kubectl -n "$namespace" delete krsession "$session" --ignore-not-found --wait=false >/dev/null
}
trap cleanup EXIT

wait_for_value() {
	resource="$1"
	jsonpath="$2"
	want="$3"
	for _ in $(seq 1 120); do
		if [ -n "$sender_pod" ]; then
			sender_phase="$(kubectl -n "$namespace" get "pod/$sender_pod" -o 'jsonpath={.status.phase}' 2>/dev/null || true)"
			if [ "$sender_phase" = Failed ] || [ "$sender_phase" = Succeeded ]; then
				kubectl -n "$namespace" logs "$sender_pod" >&2 || true
				echo "media sender stopped while waiting for $resource $jsonpath=$want" >&2
				return 1
			fi
		fi
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

wait_for_probe() {
	pod="$1"
	for _ in $(seq 1 60); do
		phase="$(kubectl -n "$namespace" get "pod/$pod" -o 'jsonpath={.status.phase}' 2>/dev/null || true)"
		case "$phase" in
		Succeeded) return 0 ;;
		Failed)
			kubectl -n "$namespace" logs "$pod" >&2 || true
			return 1
			;;
		esac
		sleep 1
	done
	echo "timed out waiting for recording probe $pod (last phase: $phase)" >&2
	return 1
}

run_protocol() {
	protocol="$1"
	probe="recording-probe-$protocol"
	sender_pod="sender-$protocol"

	kubectl apply -f test/integration/session-workloads.yaml >/dev/null
	wait_for_count deployment 2
	kubectl -n "$namespace" wait deployment -l "$selector" --for=condition=Available --timeout=120s >/dev/null

	input_service="$(kubectl -n "$namespace" get service -l "$selector" -o 'jsonpath={.items[?(@.spec.type=="NodePort")].metadata.name}')"
	test -n "$input_service"
	if [ "$protocol" = rist ]; then
		output_url="rist://$input_service:10000"
		protocol_args="-rist_profile 1"
	else
		output_url="srt://$input_service:10001?mode=caller&transtype=live"
		protocol_args=""
	fi
	# shellcheck disable=SC2086
	kubectl -n "$namespace" run "$sender_pod" --image=kinugasa-recording/video-fanout:latest --image-pull-policy=IfNotPresent --restart=Never --command -- \
		ffmpeg -hide_banner -loglevel error -re -f lavfi -i testsrc=size=320x180:rate=15 \
		-an -c:v libx264 -preset ultrafast -tune zerolatency -profile:v baseline -pix_fmt yuv420p \
		-x264-params repeat-headers=1:keyint=30 \
		-f mpegts $protocol_args "$output_url" >/dev/null

	wait_for_value "krsession/$session" '{.status.cameras[0].phase}' Connected
	wait_for_value "krsession/$session" '{.status.cameras[0].connectedProtocol}' "$protocol"
	last_frame_at="$(kubectl -n "$namespace" get "krsession/$session" -o 'jsonpath={.status.cameras[0].lastFrameAt}')"
	test -n "$last_frame_at"
	service="$(kubectl -n "$namespace" get service -l "$selector" -o 'jsonpath={.items[?(@.spec.ports[0].name=="recording")].metadata.name}')"
	test -n "$service"
	kubectl -n "$namespace" run "$probe" --image=kinugasa-recording/video-fanout:latest --image-pull-policy=IfNotPresent --restart=Never --command -- \
		ffmpeg -hide_banner -loglevel error -i "srt://$service:12000?mode=caller&transtype=live&timeout=10000000" -t 5 -map 0:v:0 -f null - >/dev/null
	wait_for_probe "$probe"
	kubectl -n "$namespace" delete pod "$probe" --wait=false >/dev/null
	stop_sender

	kubectl -n "$namespace" patch "krsession/$session" --type=merge --patch '{"spec":{"cameras":[{"name":"front","desiredState":"Absent","ingress":{"ristNodePort":31000,"srtNodePort":31001}}]}}' >/dev/null
	wait_for_value "krsession/$session" '{.status.cameras[0].phase}' Removed
	wait_for_count deployment 0
	kubectl -n "$namespace" delete krsession "$session" --wait=true >/dev/null
}

cleanup
wait_for_count deployment 0
run_protocol rist
run_protocol srt

echo "RIST/SRT preview and recording fanout integration test passed"
