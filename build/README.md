# Container builds

`make image-build`で5つのGo component imageとWeb imageをbuildする。映像系imageは
`flake.lock`で固定した`ffmpeg-headless`のclosureを取り込み、RIST、SRT、RTMP、WHIP、
MPEG-TS、segment、tee、libx264の不足をbuild中に検出する。

registryへpublishする場合は`IMAGE_PREFIX`と`IMAGE_TAG`を上書きし、deploy時の
Kustomize image overrideにも同じ値を設定する。
