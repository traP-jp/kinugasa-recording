#!/bin/sh
set -eu

cluster_name="${K3D_CLUSTER_NAME:-kinugasa-recording}"

if k3d cluster get "$cluster_name" >/dev/null 2>&1; then
	echo "k3d cluster already exists: $cluster_name"
	exit 0
fi

k3d cluster create "$cluster_name" \
	--agents 0 \
	--k3s-arg '--disable=traefik@server:0' \
	--k3s-arg '--kubelet-arg=eviction-hard=nodefs.available<2%,imagefs.available<2%@server:0' \
	--port '30080:30080@server:0' \
	--port '30081:30081@server:0' \
	--port '30082:30082@server:0' \
	--port '7881:7881@server:0' \
	--port '7882:7882/udp@server:0' \
	--port '31000-31099:31000-31099/udp@server:0'
