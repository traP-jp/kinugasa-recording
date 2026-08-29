# 機能

## console server REST API

console serverは、web consoleからシステムを操作するための内部REST APIを提供する。

### Session

| メソッド | パス | 機能 |
| --- | --- | --- |
| `GET` | `/api/sessions` | Sessionの一覧を取得する。 |
| `POST` | `/api/sessions` | Sessionを作成する。 |
| `GET` | `/api/sessions/{sessionName}` | Sessionの詳細を取得する。 |

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
| `PUT` | `/api/sessions/{sessionName}/ongoing-take` | OngoingTakeを作成し、録画を開始する。 |
| `DELETE` | `/api/sessions/{sessionName}/ongoing-take` | 録画を停止し、OngoingTakeからFinishedTakeへ遷移させる。 |
| `GET` | `/api/sessions/{sessionName}/takes` | FinishedTakeの一覧を取得する。 |
| `GET` | `/api/sessions/{sessionName}/takes/{takeName}` | FinishedTakeとVideoFileの詳細を取得する。 |

### preview

| メソッド | パス | 機能 |
| --- | --- | --- |
| `POST` | `/api/sessions/{sessionName}/preview-access` | LiveKitの接続先と短期アクセストークンを取得する。 |

- APIのリソース指定には、内部の識別子ではなくユーザーが指定したnameを使用する。
- OngoingTakeはSessionごとに最大1つのsingleton resourceとして扱う。
- OngoingTakeの作成時には、CameraConnectionを持つcameraを1つ以上指定しなければならない。
- 録画停止後のVideoFileの作成、ハッシュ計算およびアップロードは非同期に実行する。
- APIがエラーを返す場合は、web consoleで確認できる人が読めるエラー事由を含めなければならない。

## reconciliation

- console serverは、DBに永続化されたドメイン状態を正としてKubernetes上のリソースをreconcileする。
- console serverの起動時および再起動時には、DBから状態を復元して全リソースをreconcileする。
- console serverが停止している間も、video gateway、video hubおよびvideo uploaderは処理を継続する。console serverの停止だけを理由にOngoingTakeを終了してはならない。
- video hubの予期しない停止を検出した場合、対応するOngoingTakeがあればerroredなFinishedTakeへ遷移させる。その他のドメイン状態は維持したままvideo hubを再起動する。
- 再起動したvideo hubが新しいUUIDを通知した場合、対応するSessionのvideoHubIdを更新する。
