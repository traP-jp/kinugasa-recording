#!/usr/bin/env bash
set -euo pipefail

container="${1:-k3d-kinugasa-recording-v2-serverlb}"
template_path=/etc/confd/templates/nginx.tmpl
temporary_directory="$(mktemp -d)"
template_file="${temporary_directory}/nginx.tmpl"

cleanup() {
  rm -rf "${temporary_directory}"
}
trap cleanup EXIT

if ! docker inspect "${container}" >/dev/null 2>&1; then
  echo "k3d server load balancer container not found: ${container}" >&2
  exit 1
fi

docker cp "${container}:${template_path}" "${template_file}"

# k3d's shared UDP listen socket can dispatch one client flow across multiple
# Nginx workers. A single worker preserves one upstream session per UDP 5-tuple.
# This also avoids the file descriptor cost of reuseport across the whole RIST
# NodePort range.
needs_restart=false
if grep -Fqx 'worker_processes auto;' "${template_file}"; then
  sed -i 's/^worker_processes auto;$/worker_processes 1;/' "${template_file}"
  needs_restart=true
elif ! grep -Fqx 'worker_processes 1;' "${template_file}"; then
  echo "unsupported worker_processes setting in ${container}" >&2
  exit 1
fi
if grep -Fq ' udp reuseport{{- end -}};' "${template_file}"; then
  sed -i 's/ udp reuseport{{- end -}};/ udp{{- end -}};/' "${template_file}"
  needs_restart=true
fi

if ! grep -Fqx 'worker_processes 1;' "${template_file}"; then
  echo "unsupported k3d nginx template in ${container}" >&2
  exit 1
fi

if [[ "${needs_restart}" == true ]]; then
  docker cp "${template_file}" "${container}:${template_path}"
  docker restart "${container}" >/dev/null
fi

if ! docker exec "${container}" nginx -T 2>&1 \
  | grep -Fq 'worker_processes 1;'; then
  echo "single-worker UDP session affinity was not applied" >&2
  exit 1
fi

if ! docker exec "${container}" nginx -T 2>&1 \
  | grep -Eq 'listen +32000 udp;'; then
  echo "RIST listener on UDP port 32000 is unavailable" >&2
  exit 1
fi

echo "single-worker UDP session affinity is active in ${container}"
