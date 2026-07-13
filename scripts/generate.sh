#!/usr/bin/env bash

set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repository_root}"

controller-gen \
  object:headerFile="scripts/boilerplate.go.txt" \
  crd:headerFile="scripts/boilerplate.go.txt" \
  paths="./api/..." \
  output:crd:artifacts:config=config/crd/bases

client-gen \
  --go-header-file scripts/boilerplate.go.txt \
  --input-base github.com/comavius/kinugasa-recording/api \
  --input recording/v1alpha1 \
  --output-dir api/generated/clientset \
  --output-pkg github.com/comavius/kinugasa-recording/api/generated/clientset \
  --clientset-name versioned
