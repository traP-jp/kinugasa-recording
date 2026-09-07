# Local LiveKit

`deploy/local/livekit`は、local / k3dでLiveKit previewを動かすためのKustomize overlayである。LiveKit server、comavius/ingress、Redisの共通定義は`deploy/base/livekit`に置き、このoverlayではlocal向けのNodePortとadvertised IPだけを上書きする。

```console
kubectl apply -k deploy/local/livekit
```

## Ports

local k3dでは次の対応を想定する。

| 用途 | Cluster内port | NodePort / advertised port |
| --- | ---: | ---: |
| LiveKit signaling | 7880 | 30780 |
| LiveKit RTC TCP | 7881 | 31877 |

console-serverがLiveKit APIへ接続する内部URLと、ブラウザに返す公開URLは別に設定する。

```text
LIVEKIT_URL=ws://livekit:7880
LIVEKIT_PUBLIC_URL=ws://127.0.0.1:7880
```

LiveKitはRTC candidateのadvertise portをlisten portから独立して指定する設定を持たない。そのため、local NodePort経由でブラウザ受信を成立させるには、`LIVEKIT_RTC_TCP_PORT`、`livekit` containerの`rtc-tcp` port、`livekit` Serviceの`rtc-tcp.nodePort`を同じ値にする。

`NODE_IP`は`kustomization.yaml`の`livekit-runtime-env` ConfigMap generatorで指定する。k3d nodeのIPが変わった場合はこの値を更新する。
