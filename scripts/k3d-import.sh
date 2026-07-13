#!/bin/sh
set -eu

cluster_name="${K3D_CLUSTER_NAME:-kinugasa-recording}"
image_prefix="${IMAGE_PREFIX:-kinugasa-recording}"
image_tag="${IMAGE_TAG:-latest}"

set --
for component in operator video-fanout video-recorder video-uploader livekit-ingress web; do
	set -- "$@" "$image_prefix/$component:$image_tag"
done
k3d image import --cluster "$cluster_name" "$@"
