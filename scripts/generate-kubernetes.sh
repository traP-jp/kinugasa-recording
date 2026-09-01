#!/usr/bin/env bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

go tool controller-gen object paths=./api/...
go tool controller-gen \
  crd:generateEmbeddedObjectMeta=true \
  paths=./api/... \
  output:crd:artifacts:config=deploy/crds
go tool controller-gen \
  rbac:roleName=kinugasa-recording-operator \
  paths=./internal/operator/... \
  output:rbac:artifacts:config=deploy/rbac
