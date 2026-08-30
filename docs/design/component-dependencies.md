# コンポーネント間の依存関係

v2要件で定義された論理コンポーネントと外部依存を示す。実線は実行時の通信またはデータの流れ、破線はconsole serverによるKubernetesリソースのライフサイクル管理、太線は必要だが通信方式が未定義の状態反映を表す。矢印は呼び出し、送信または書き込みの向きである。

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
            worker["video worker<br/>MediaMTX"]
            uploader["video uploader"]
            volume[("shared volume<br/>PersistentVolumeClaim")]

            worker -->|"確定済みMP4を書き込む"| volume
            volume -->|"録画ファイルを読み出す"| uploader
        end
    end

    db[("永続DB")]
    objectStorage[("オブジェクトストレージ")]

    operator -->|"ブラウザ操作"| web
    web -->|"REST API<br/>操作・状態・preview access"| console
    livekit -->|"リアルタイムpreview"| web
    pipeline -->|"REST API<br/>lock file取得"| console

    camera -->|"RIST Main Profile<br/>H.264 + optional audio"| gateway
    gateway -->|"RTP"| worker
    worker -->|"preview映像"| livekit
    console <-->|"gRPC双方向stream<br/>command・event・状態同期"| worker
    uploader -->|"hash計算・upload"| objectStorage
    uploader ==>|"upload結果・hash・object metadata<br/>反映方式は未定義"| console

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
| video gateway / video worker            | [製品の位置づけ](../requirements-v2/08-product-perspective/product-perspective.md)                                                     | RTP中継を要求。詳細なwire契約は未定義         |
| video worker / video uploader           | [機能要求](../requirements-v2/14-specified-requirements/02-functions/functions.md)                                                     | shared volume上の確定済みファイルで連携       |
| video uploader / console server         | [論理データベース要求](../requirements-v2/14-specified-requirements/05-logical-database-requirements/logical-database-requirements.md) | VideoFileへの結果反映が必要。通信方式は未定義 |
| video uploader / オブジェクトストレージ | [製品の位置づけ](../requirements-v2/08-product-perspective/product-perspective.md)                                                     | upload責務を要求。ストレージAPI契約は未定義   |

video workerがgRPC streamを開始し、console serverがそのstream上でcommandを送り、video workerがeventとcommand resultを返す。video uploaderからconsole serverへの太線は、VideoFileとlock fileを確定するために必要となる依存を論理データモデルから補ったものであり、具体的な通信経路は要件および契約にまだ定義されていない。内部通信の認証、認可および暗号化はapplication containerでは実装せず、Istioへ委譲する。
