#!/bin/sh
set -eu

is_ipv4() {
	printf '%s\n' "$1" | awk -F. '
		NF != 4 { exit 1 }
		{
			for (i = 1; i <= 4; i++) {
				if ($i !~ /^[0-9]+$/ || $i < 0 || $i > 255) exit 1
			}
		}'
}

public_ip="${PUBLIC_HOST:-}"
if [ -z "$public_ip" ] && command -v ip >/dev/null 2>&1; then
	public_ip="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i = 1; i <= NF; i++) if ($i == "src") {print $(i + 1); exit}}')"
fi
if [ -z "$public_ip" ] && command -v ip >/dev/null 2>&1; then
	public_ip="$(ip -o -4 address show scope global 2>/dev/null | awk '{split($4, address, "/"); print address[1]; exit}')"
fi
if [ -z "$public_ip" ] || ! is_ipv4 "$public_ip"; then
	echo "LAN IPv4 address could not be detected; set PUBLIC_HOST explicitly" >&2
	exit 2
fi

printf '%s\n' "$public_ip"
