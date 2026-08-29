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
| `DELETE` | `/api/sessions/{sessionName}/cameras/{cameraName}` | cameraを削除する。 |
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

## reconciliation

- console serverは、DBに永続化されたドメイン状態を正としてKubernetes上のリソースをreconcileする。
- console serverの起動時および再起動時には、DBから状態を復元して全リソースをreconcileする。
- console serverが停止している間も、video gateway、video hubおよびvideo uploaderは処理を継続する。console serverの停止だけを理由にOngoingTakeを終了してはならない。
- video hubの予期しない停止を検出した場合、対応するOngoingTakeがあればerroredなFinishedTakeへ遷移させる。その他のドメイン状態は維持したままvideo hubを再起動する。
- 再起動したvideo hubが新しいUUIDを通知した場合、対応するSessionのvideoHubIdを更新する。
