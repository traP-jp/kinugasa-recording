# Media integration smoke test

このtestは、FFmpegの合成H.264/30 fps映像を使い、次のprocess間経路を検証する。

```text
FFmpeg RIST sender -> Rust video gateway (rist-rs/librist) -> RTP/MP2T -> video worker -> MediaMTX -> RTSP -> FFmpeg decoder
```

送受信間ではAES-256 PSKを使用する。さらに、Rust video gatewayが5秒区間のRIST統計をOTLP/gRPCで送信し、console serverのreceiverが受信できることを確認する。録画品質、frame drop、camera間driftは評価しない。FFmpeg、FFprobeおよびMediaMTXはNix development shellに含まれる。video gatewayは先にbuildしておく。

```console
nix develop -c cargo build --locked --manifest-path video-gateway/Cargo.toml
nix develop -c go test -tags=integration -v ./tests/integration
```

MediaMTX 1.20以降では、LiveKitプレビュー用の経路がH.264映像とOpus音声になることも検証する。別のMediaMTX binaryを使う場合は、`KINUGASA_MEDIAMTX_BINARY`にpathを指定する。

別のvideo gateway binaryを使用する場合は、`KINUGASA_VIDEO_GATEWAY_BINARY`にそのpathを指定する。
