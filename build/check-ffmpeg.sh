#!/bin/sh
set -eu

protocols="$(ffmpeg -hide_banner -protocols 2>/dev/null)"
muxers="$(ffmpeg -hide_banner -muxers 2>/dev/null)"
encoders="$(ffmpeg -hide_banner -encoders 2>/dev/null)"

for protocol in rist srt rtmp; do
	echo "$protocols" | grep -Eq "^[[:space:]]+$protocol$" || {
		echo "ffmpeg protocol is missing: $protocol" >&2
		exit 1
	}
done

for muxer in flv mpegts segment tee whip; do
	echo "$muxers" | grep -Eq "^[[:space:]]*E[[:space:]].*[[:space:]]$muxer([,[:space:]]|$)" || {
		echo "ffmpeg muxer is missing: $muxer" >&2
		exit 1
	}
done

echo "$encoders" | grep -Eq "[[:space:]]libx264[[:space:]]" || {
	echo "ffmpeg encoder is missing: libx264" >&2
	exit 1
}
