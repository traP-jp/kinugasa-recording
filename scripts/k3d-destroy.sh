#!/bin/sh
set -eu

k3d cluster delete "${K3D_CLUSTER_NAME:-kinugasa-recording}"
