# コンポーネント間の依存関係

v2要件で定義された論理コンポーネントと外部依存を示す。実線は実行時の通信またはデータの流れ、破線はconsole serverによるKubernetesリソースのライフサイクル管理を表す。矢印は呼び出し、送信または書き込みの向きである。

```mermaid
flowchart LR
    operator["収録オペレーター"]
    camera["cameraクライアント"]
    pipeline["後段パイプライン"]

    subgraph cluster["Kubernetesクラスタ"]
        web["web console<br/>TypeScript / React"]
        console["console server<br/>Go / Kubernetes Operator"]
        gateway["video gateway<br/>ristreceiver / librist"]
        livekit["LiveKit"]
        kubeapi["Kubernetes API"]

        subgraph workerPod["CameraConnectionごとのvideo worker Pod"]
            worker["video worker<br/>MediaMTX / upload"]
            volume[("work volume<br/>PersistentVolumeClaim")]

            worker -->|"確定済みMP4を書き込む"| volume
            volume -->|"録画ファイルを読み出す"| worker
        end
    end

    db[("永続DB")]
    objectStorage[("オブジェクトストレージ")]

    operator -->|"ブラウザ操作"| web
    web -->|"REST API<br/>操作・状態・preview access"| console
    livekit -->|"リアルタイムpreview"| web
    pipeline -->|"REST API<br/>lock file取得"| console

    camera -->|"RIST Main Profile<br/>H.264 + optional audio"| gateway
    gateway -->|"RTP / MP2T<br/>payload type 33"| worker
    worker -->|"WHIP<br/>preview映像"| livekit
    console -->|"Ingress API<br/>camera ingress作成・削除"| livekit
    console <-->|"gRPC双方向stream<br/>command・event・状態同期"| worker
    worker -->|"hash計算・upload"| objectStorage
    worker -->|"gRPC<br/>upload結果・hash・object metadata"| console

    console <-->|"domain state"| db
    console <-->|"resource作成・監視・reconcile"| kubeapi
    kubeapi -.->|"lifecycle管理"| gateway
    kubeapi -.->|"Pod・PVCのlifecycle管理"| workerPod
```

## 契約との対応

| 境界                                    | 契約または根拠                                                                                                                         | 状態                                          |
| --------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------- |
| web console / console server            | [`contracts/console-api/openapi.yaml`](../../contracts/console-api/openapi.yaml)                                                       | OpenAPIで定義済み                             |
| console server / video worker           | [`contracts/console-video-worker/v1/console_video_worker.proto`](../../contracts/console-video-worker/v1/console_video_worker.proto)   | gRPCと再送規則を定義済み                      |
| console server / 後段パイプライン       | [`contracts/lockfile/lockfile.schema.json`](../../contracts/lockfile/lockfile.schema.json)                                             | JSON Schemaで定義済み                         |
| cameraクライアント / video gateway      | [外部インターフェース要求](../requirements-v2/14-specified-requirements/01-external-interfaces/external-interfaces.md)                 | RIST、H.264、30 fpsを要求                     |
| video gateway / video worker            | [製品の位置づけ](../requirements-v2/08-product-perspective/product-perspective.md)                                                     | `ristreceiver`が復旧したRTP/MP2TをUDPで中継   |
| video worker / オブジェクトストレージ   | [製品の位置づけ](../requirements-v2/08-product-perspective/product-perspective.md)                                                     | upload責務を要求。ストレージAPI契約は未定義   |

video workerがgRPC streamを開始し、console serverがそのstream上でcommandを送り、video workerがeventとcommand resultを返す。video workerはterminalなupload結果も同じgRPC serviceで冪等に報告する。内部通信の認証、認可および暗号化はapplication containerでは実装せず、Istioへ委譲する。

video gateway containerはGo製runtimeを持たず、`ristreceiver`を直接実行する。video workerはRTP/MP2TからRTP transport headerを除去してMediaMTXのUDP MPEG-TS inputへ渡す。MPEG-TSのdemux、配信およびfMP4録画はMediaMTXが行い、video workerはMediaMTXのRTSP出力に対して映像形式とframe rateを検証する。
