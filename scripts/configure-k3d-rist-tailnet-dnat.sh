#!/usr/bin/env bash
set -euo pipefail

tailnet_address="${1:-100.79.104.2}"
node_container="${2:-k3d-kinugasa-recording-v2-server-0}"
tailnet_interface="${3:-tailscale0}"
port_min=32000
port_max=32099
network_tool_image=nicolaka/netshoot:v0.14
rule_comment=kinugasa-rist-tailnet

if ! docker inspect "${node_container}" >/dev/null 2>&1; then
  echo "k3d server node container not found: ${node_container}" >&2
  exit 1
fi

node_address="$({
  docker inspect "${node_container}" \
    --format '{{range .NetworkSettings.Networks}}{{println .IPAddress}}{{end}}'
} | awk 'NF {print; exit}')"
if [[ -z "${node_address}" ]]; then
  echo "could not resolve the address of ${node_container}" >&2
  exit 1
fi

docker run --rm --privileged --network host -i "${network_tool_image}" sh -s -- \
  "${tailnet_interface}" "${tailnet_address}" "${node_address}" \
  "${port_min}" "${port_max}" "${rule_comment}" <<'SCRIPT'
set -eu

tailnet_interface=$1
tailnet_address=$2
node_address=$3
port_min=$4
port_max=$5
rule_comment=$6
port_range="${port_min}:${port_max}"
changed=false

# Remove a rule left by the earlier single-port workaround, if present.
while iptables -t nat -C PREROUTING \
  -i "${tailnet_interface}" -d "${tailnet_address}/32" -p udp \
  --dport "${port_min}" -j DNAT --to-destination "${node_address}:${port_min}" \
  2>/dev/null; do
  iptables -t nat -D PREROUTING \
    -i "${tailnet_interface}" -d "${tailnet_address}/32" -p udp \
    --dport "${port_min}" -j DNAT --to-destination "${node_address}:${port_min}"
  changed=true
done

if ! iptables -t nat -C PREROUTING \
  -i "${tailnet_interface}" -d "${tailnet_address}/32" -p udp \
  --dport "${port_range}" -m comment --comment "${rule_comment}" \
  -j DNAT --to-destination "${node_address}" 2>/dev/null; then
  iptables -t nat -I PREROUTING 1 \
    -i "${tailnet_interface}" -d "${tailnet_address}/32" -p udp \
    --dport "${port_range}" -m comment --comment "${rule_comment}" \
    -j DNAT --to-destination "${node_address}"
  changed=true
fi

# Existing Docker conntrack entries keep their old destination until removed.
if [ "${changed}" = true ]; then
  conntrack -D -p udp --orig-dst "${tailnet_address}" \
    --dport "${port_range}" >/dev/null 2>&1 || true
fi

iptables -t nat -C PREROUTING \
  -i "${tailnet_interface}" -d "${tailnet_address}/32" -p udp \
  --dport "${port_range}" -m comment --comment "${rule_comment}" \
  -j DNAT --to-destination "${node_address}"
SCRIPT

echo "${tailnet_address}:${port_min}-${port_max}/udp now maps directly to ${node_address}:${port_min}-${port_max}/udp"
