# 機能

## console server REST API

console serverは、web consoleからシステムを操作するための内部REST APIと、後段パイプラインへ録画ファイルのlock fileを提供するREST APIを提供する。

REST APIのエンドポイント、リクエスト、レスポンスおよびエラーの詳細は、[console API contract](../../../../contracts/console-api/openapi.yaml)に定義する。

### Session

| メソッド | パス | 機能 |
| --- | --- | --- |
| `GET` | `/api/sessions` | Sessionの一覧を取得する。 |
| `POST` | `/api/sessions` | Sessionを作成する。 |
| `GET` | `/api/sessions/{sessionName}` | Sessionの詳細と、存在する場合はOngoingTakeの名前を取得する。 |

### 後段パイプライン

| メソッド | パス | 機能 |
| --- | --- | --- |
| `GET` | `/api/sessions/{sessionName}/lockfile` | Sessionに属するアップロード済み録画ファイルのlock fileを取得する。 |

### camera

| メソッド | パス | 機能 |
| --- | --- | --- |
| `GET` | `/api/sessions/{sessionName}/cameras` | 現在のCameraConnectionの一覧を取得する。 |
| `POST` | `/api/sessions/{sessionName}/cameras` | cameraを追加する。 |
| `DELETE` | `/api/sessions/{sessionName}/cameras/{cameraName}` | cameraを削除する。uploadingなVideoFileがある場合の強制削除を含む。 |
| `GET` | `/api/sessions/{sessionName}/cameras/{cameraName}/connection` | cameraクライアントの接続先と接続状態を取得する。 |

### take

| メソッド | パス | 機能 |
| --- | --- | --- |
| `GET` | `/api/sessions/{sessionName}/ongoing-take` | OngoingTakeを取得する。 |
| `POST` | `/api/sessions/{sessionName}/ongoing-take/start` | OngoingTakeを作成し、録画を開始する。 |
| `POST` | `/api/sessions/{sessionName}/ongoing-take/finish` | 録画を終了し、OngoingTakeからFinishedTakeへ遷移させる。 |
| `GET` | `/api/sessions/{sessionName}/takes` | FinishedTakeの一覧を取得する。 |
| `GET` | `/api/sessions/{sessionName}/takes/{takeName}` | FinishedTakeとVideoFileの詳細を取得する。 |

### preview

| メソッド | パス | 機能 |
| --- | --- | --- |
| `POST` | `/api/sessions/{sessionName}/preview-access` | LiveKitの接続先と短期アクセストークンを取得する。 |

## 録画ファイルの保持とupload

- console serverは、video worker Podの作成に先立ってPodごとに1つのPersistentVolumeClaimを作成し、work volumeとしてPod specから静的に参照させる。takeの開始ごとにPersistentVolumeClaimを作成してはならない。
- video workerは、録画ファイルをwork volumeへ書き込み、録画終了時にflushしてファイルを確定してから、同じprocessでハッシュの計算とオブジェクトストレージへのuploadを行う。
- video workerの録画処理とupload処理は同じapplication container内で動作する。独立したvideo uploader processまたはcontainerを作成してはならない。
- 対応するCameraIdentityにuploadingなVideoFileが存在する間、console serverは通常のcamera削除を拒否する。強制削除が明示的に指定された場合に限り、削除を受け付ける。
- console serverは強制削除の受付時に、対象のvideo workerが処理しているすべてのuploadingなVideoFileをerroredに遷移させ、そのuploadをabortしてからPod、PersistentVolumeClaimおよびCameraConnectionを削除する。
- 強制削除によってabortされたuploadについては、オブジェクトストレージ上のobjectまたはmultipart uploadの存在、完全性および後始末を保証しない。
- 強制削除しない場合、video worker PodとPersistentVolumeClaimは、対応するすべてのVideoFileがcompletedまたはerroredになるまで削除してはならない。

## console server - video worker間通信

console serverとvideo worker間のcommand、event、状態同期および障害時の再送規則は、[console-server - video-worker gRPC contract](../../../../contracts/console-video-worker/README.md)に定義する。

## reconciliation

- console serverは、DBに永続化されたドメイン状態を正としてKubernetes上のリソースをreconcileする。
- console serverの起動時および再起動時には、DBから状態を復元して全リソースをreconcileする。
- console serverが停止している間も、video gatewayおよびvideo workerは録画、preview中継およびuploadを継続する。console serverの停止だけを理由にOngoingTakeを終了してはならない。
- video workerの予期しない停止を検出した場合、対応するRecordingCameraがあればそのRecordingCameraだけをerroredとし、そのvideo workerが処理していたすべてのuploadingなVideoFileもerroredに遷移させる。OngoingTake、他のRecordingCameraおよび他のVideoFileの状態は維持したままvideo workerを再起動する。erroredに遷移したuploadは再開しない。
- 再起動したvideo workerが新しいUUIDを通知した場合、対応するCameraConnectionのvideoWorkerIdを更新する。
