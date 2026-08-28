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
- 録画停止後のVideoFileの作成、ハッシュ計算およびアップロードは非同期に実行する。
