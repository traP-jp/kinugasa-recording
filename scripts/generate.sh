#!/usr/bin/env bash

set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repository_root}"

controller-gen \
  object:headerFile="scripts/boilerplate.go.txt" \
  crd:headerFile="scripts/boilerplate.yaml.txt" \
  paths="./api/..." \
  output:crd:artifacts:config=config/crd/bases

client_generation_root="$(mktemp -d)"
trap 'rm -rf "${client_generation_root}"' EXIT

client-gen \
  --go-header-file scripts/boilerplate.go.txt \
  --input-base github.com/comavius/kinugasa-recording/api \
  --input recording/v1alpha1 \
  --output-base "${client_generation_root}" \
  --output-package github.com/comavius/kinugasa-recording/api/generated/clientset \
  --clientset-name versioned

rm -rf api/generated/clientset
mkdir -p api/generated
cp -R \
  "${client_generation_root}/github.com/comavius/kinugasa-recording/api/generated/clientset" \
  api/generated/clientset
