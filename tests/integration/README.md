# Media integration smoke test

このtestは、FFmpegの合成H.264/30 fps映像を使い、次のprocess間経路を検証する。

```text
FFmpeg RIST sender -> video gateway (ristreceiver) -> RTP/MP2T -> video worker -> MediaMTX -> RTSP -> FFmpeg decoder
```

録画品質、frame drop、camera間driftは評価しない。必要なbinaryはNix development shellに含まれる。

```console
nix develop -c go test -tags=integration -v ./tests/integration
```
