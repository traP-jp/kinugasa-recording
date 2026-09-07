# Local LiveKit

`deploy/local/livekit`は、local / k3dでLiveKit previewを動かすためのKustomize overlayである。production向けの`deploy/base`は外部LiveKit利用を前提にしているため、このoverlayは独立して適用する。LiveKit server、comavius/ingress、Redisを含む。

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

`nodeIP`は`kustomization.yaml`の`livekit-local-public` ConfigMap generatorで指定し、`NODE_IP`環境変数へ反映する。k3d nodeのIPが変わった場合はこの値を更新する。
