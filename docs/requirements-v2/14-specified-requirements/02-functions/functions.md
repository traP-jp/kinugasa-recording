# 機能

## console server REST API

console serverは、web consoleからシステムを操作するための内部REST APIを提供する。

### Session

| メソッド | パス | 機能 |
| --- | --- | --- |
| `GET` | `/api/sessions` | Sessionの一覧を取得する。 |
| `POST` | `/api/sessions` | Sessionを作成する。 |
| `GET` | `/api/sessions/{sessionName}` | Sessionの詳細と、存在する場合はOngoingTakeの名前を取得する。 |

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

- APIのリソース指定には、内部の識別子ではなくユーザーが指定したnameを使用する。
- OngoingTakeはSessionごとに最大1つのsingleton resourceとして扱う。
- Sessionの詳細では、OngoingTakeが存在しない場合、ongoingTakeNameをnullとして返す。
- OngoingTakeの取得結果は、OngoingTakeの有無を判別できるtagged unionとして返す。OngoingTakeが存在しない場合は正常系として扱い、`200 OK`を返す。Session自体が存在しない場合は`404 Not Found`を返す。
- OngoingTakeの作成時には、CameraConnectionを持つcameraを1つ以上指定しなければならない。
- OngoingTakeが存在しない状態で録画終了を要求した場合は、`409 Conflict`を返す。
- 録画停止後のVideoFileの作成、ハッシュ計算およびアップロードは非同期に実行する。
- APIがエラーを返す場合は、web consoleで確認できる人が読めるエラー事由を含めなければならない。
- Session一覧とFinishedTake一覧は、1から始まる`page`と1ページあたりの件数を表す`pageSize`を指定するページ番号方式でページネーションする。`page`のデフォルト値は1、`pageSize`のデフォルト値は20、最大値は100とする。
- Session一覧は`createdAt`の降順、同値の場合は`name`の昇順で返す。
- FinishedTake一覧は`finishedAt`の降順、同値の場合は`name`の昇順で返す。
- その他のAPIはページネーションしない。

## reconciliation

- console serverは、DBに永続化されたドメイン状態を正としてKubernetes上のリソースをreconcileする。
- console serverの起動時および再起動時には、DBから状態を復元して全リソースをreconcileする。
- console serverが停止している間も、video gateway、video hubおよびvideo uploaderは処理を継続する。console serverの停止だけを理由にOngoingTakeを終了してはならない。
- video hubの予期しない停止を検出した場合、対応するOngoingTakeがあればerroredなFinishedTakeへ遷移させる。その他のドメイン状態は維持したままvideo hubを再起動する。
- 再起動したvideo hubが新しいUUIDを通知した場合、対応するSessionのvideoHubIdを更新する。
