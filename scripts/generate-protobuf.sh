#!/usr/bin/env bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

buf lint
protoc \
  -I contracts/console-video-uploader \
  --go_out=. \
  --go_opt=module=github.com/traP-jp/kinugasa-recording \
  --go-grpc_out=. \
  --go-grpc_opt=module=github.com/traP-jp/kinugasa-recording \
  contracts/console-video-uploader/v1/console_video_uploader.proto
protoc \
  -I contracts/console-video-worker \
  --go_out=. \
  --go_opt=module=github.com/traP-jp/kinugasa-recording \
  --go-grpc_out=. \
  --go-grpc_opt=module=github.com/traP-jp/kinugasa-recording \
  contracts/console-video-worker/v1/console_video_worker.proto
