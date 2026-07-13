#!/bin/sh
set -eu

namespace="${KINUGASA_NAMESPACE:-kinugasa-recording}"
session="session-integration"
selector="recording.kinugasa.tra.pt/session=$session"

cleanup() {
	kubectl -n "$namespace" delete session "$session" --ignore-not-found --wait=false >/dev/null
}
trap cleanup EXIT

wait_for_value() {
	resource="$1"
	jsonpath="$2"
	want="$3"
	for _ in $(seq 1 120); do
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

cleanup
kubectl apply -f test/integration/session-workloads.yaml >/dev/null

wait_for_count deployment 2
wait_for_count service 3
wait_for_count secret 1
kubectl -n "$namespace" wait deployment -l "$selector" --for=condition=Available --timeout=120s >/dev/null
wait_for_value "session/$session" '{.status.cameras[0].phase}' Waiting

kubectl -n "$namespace" patch "session/$session" --type=merge --patch '{"spec":{"cameras":[{"name":"front","desiredState":"Absent","ingress":{"ristNodePort":31000,"srtNodePort":31001}}]}}' >/dev/null
wait_for_value "session/$session" '{.status.cameras[0].phase}' Removed
wait_for_count deployment 0
wait_for_count service 0
wait_for_count secret 0

echo "Session camera workload integration test passed"
