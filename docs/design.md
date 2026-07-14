# kinugasa-recording 初期設計

## 1. この文書の位置付け

この文書は`docs/requirements.md`を実装へ落とすための初期設計である。要件と矛盾する場合は要件を優先する。
時刻はすべてUTCのRFC 3339形式、HTTPのJSON fieldはlower camel case、Custom ResourceのfieldはKubernetes API conventionに従う。

## 2. Session Custom Resource

### 2.1 基本形

- API group/version/kindは`recording.kinugasa.tra.pt/v1alpha1`, `Session`とする。
- namespace scopeとし、1 Session CRが1つのsessionを表す。
- 利用者が指定した名前は`spec.name`へ保存し、作成後は変更不可とする。大文字を含む名前や先頭がハイフンの名前はKubernetes resource名にできないため、`metadata.name`は名前のUTF-8 byte列のSHA-256をlower-case base32で符号化した`session-<digest>`とする。
- Web APIによるSession削除は提供しない。誤ってCRが削除されても名前を再利用しないため、Session作成時にS3へ予約objectを作成する。
- `spec`は利用者が要求するdesired state、`status`はreconcilerが観測した状態とする。reconcilerは`spec`を変更しない。

概念上のschemaは次の通りとする。実際のGo型とCRDではKubernetesの標準型（`metav1.Condition`、`metav1.Time`等）を用いる。

```yaml
spec:
  name: session-1
  reservedCameraNames: [front, side]
  reservedTakeNames: [take-1]
  cameras:
    - name: front
      desiredState: Present       # Present | Absent
      ingress:
        ristNodePort: 31000
        srtNodePort: 31001
  takes:
    - name: take-1
      desiredState: Stopped       # Recording | Stopped
      cameraNames: [front]
      requestedAt: "2026-07-14T01:00:00Z"
      stopRequestedAt: "2026-07-14T01:10:00Z"
status:
  observedGeneration: 4
  phase: Ready                     # Pending | Ready | Recording | Degraded
  cameras:
    - name: front
      phase: Connected             # Provisioning | Waiting | Connected | Disconnected | Deleting | Removed | Error
      connectedProtocol: srt       # rist | srt。未接続時は省略
      lastFrameAt: "2026-07-14T01:09:59Z"
      endpoints:
        rist: rist://192.0.2.10:31000
        srt: srt://192.0.2.10:31001?mode=caller&transtype=live
      conditions: []
  takes:
    - name: take-1
      phase: Uploading              # Pending | Starting | Recording | Stopping | Uploading | Completed | Failed
      cameras:
        - name: front
          recorderPhase: Stopped    # Pending | Running | Stopped | Failed
          uploadPhase: Uploading    # Pending | Uploading | Completed | Failed
          discoveredFiles: 10
          uploadedFiles: 9
          pendingFiles: 1
          failedFiles: 0
          lastUploadedObjectKey: session-1/take-1/front/segment-00000000000000000008.ts
          conditions: []
      conditions: []
  conditions: []
```

配列要素は`name`をmap keyとして扱う。API更新では同一Sessionに対してKubernetesの`resourceVersion`による楽観的排他を行い、競合時は再取得してvalidationからやり直す。

### 2.2 使用済み名称

- `reservedCameraNames`と`reservedTakeNames`は追加のみ可能な履歴であり、対象を削除・完了しても消さない。
- Kubernetes CEL validationの計算量と単一CRのsizeを制限するため、1 Sessionあたりのcamera履歴、take履歴、camera定義、take定義、1 takeのcamera選択はそれぞれ最大100件とする。上限到達後は新しいSessionを作成する。
- camera/takeの作成は、対応する予約済み名称への追加と定義の追加を1回のCR更新で行う。
- cameraは削除後も`desiredState: Absent`のtombstoneを残す。takeは監査とupload状態の保持のため完了後も残す。
- Session名は、決定的に生成したCR名の存在確認に加えてS3の`<session>/.kinugasa-session`を予約objectとして扱う。Session作成APIはprefixまたは予約objectが存在すれば拒否し、存在しなければ`If-None-Match: *`相当の条件付きPUTで予約objectを作ってからCRを作る。利用するS3互換実装で条件付きPUTを提供できない場合は、leader electionで書き込み元を1つにしたOperator内で同じ判定を直列化する。
- S3予約後にCR作成が失敗した場合、予約objectは削除しない。安全側に倒して名前を使用済みとし、運用者が原因を調査できるeventを記録する。

### 2.3 状態遷移

Cameraの基本遷移は次の通りである。

```text
追加: Provisioning -> Waiting -> Connected <-> Disconnected
削除: (Waiting | Connected | Disconnected | Error) -> Deleting -> Removed
異常: Provisioning -> Error
```

`Connected`はfanoutが選択された入力から継続的にpacket/frameを観測している状態とする。設定可能な無受信時間を超えると`Disconnected`にする。take録画中の削除要求はHTTP APIで拒否する。

Takeの基本遷移は次の通りである。

```text
開始: Pending -> Starting -> Recording
停止: Recording -> Stopping -> Uploading -> Completed
起動不能: Pending | Starting -> Failed
```

camera切断またはuploadの一時的失敗ではtakeを自動停止せず、phaseを維持してConditionを追加する。録画processの異常終了は当該cameraを`recorderPhase: Failed`にするが、他cameraとtake全体は停止しない。停止後に未upload fileが残る場合は`Uploading`のままretryし、期限超過時は`UploadIncomplete=True`を設定する。すべての対象cameraでrecorder終了とupload完了を観測した場合だけ`Completed`にする。

## 3. 名称validation

session、camera、takeへ共通に、正規表現`^[A-Za-z0-9-]+$`を適用する。これにより要件どおり英数字とハイフンだけを許可する。空文字は許可せず、実装上の上限は255 byte、大小文字は区別する。URL pathで扱う場合は必ずpercent encodeする。

判定はWeb UIだけに依存せず、HTTP APIとCRD CEL validationの両方で行う。HTTP APIでは次の順に検証する。

1. 形式と長さ。
2. 現在の定義および予約済み名称との重複。
3. Sessionの場合はS3の予約objectおよび`<session>/` prefixの存在。
4. CR更新時の`resourceVersion`競合を考慮し、競合したら再取得して再判定。

## 4. Web UI向けHTTP API

### 4.1 共通規則

- base pathは`/api/v1`、media typeは`application/json`とする。
- 作成は`201 Created`、desired stateを変更して非同期reconcileする操作は`202 Accepted`、取得は`200 OK`を返す。
- mutation requestは`Idempotency-Key` headerを受け付ける。同じkeyと同じbodyの再送は最初の応答を返し、異なるbodyなら`409 Conflict`とする。
- 応答中の`session`は第2章のCRをWeb向けに整形したresourceであり、秘密情報と内部endpointを含めない。

### 4.2 endpoint

#### Session作成

```http
POST /api/v1/sessions
{"name":"session-1"}
```

成功応答は`201`と`{"session": <SessionResource>}`。名前の現在・過去の重複は`409 NAME_RESERVED`とする。

#### Session一覧・状態取得

```http
GET /api/v1/sessions
GET /api/v1/sessions/{sessionName}
```

一覧は`{"sessions":[<SessionSummary>]}`、個別取得は`{"session":<SessionResource>}`を返す。個別resourceにはcamera/takeのdesired state、observed state、conditions、公開接続URLを含める。

#### Camera追加

```http
POST /api/v1/sessions/{sessionName}/cameras
{"name":"front"}
```

成功応答は`202`と次の形にする。portは作成時に確保してCRへ保存するため、reconcileやOperator再起動で変化しない。

```json
{
  "camera": {"name":"front","phase":"Provisioning"},
  "connectionUrls": {
    "rist":"rist://192.0.2.10:31000",
    "srt":"srt://192.0.2.10:31001?mode=caller&transtype=live"
  }
}
```

#### Camera削除

```http
DELETE /api/v1/sessions/{sessionName}/cameras/{cameraName}
```

成功応答は`202`と`{"camera":{"name":"front","phase":"Deleting"}}`。録画中は`409 TAKE_RECORDING`とする。停止完了はSession状態の`Removed`で確認する。

#### Take開始

```http
POST /api/v1/sessions/{sessionName}/takes
{"name":"take-1","cameraNames":["front","side"]}
```

`cameraNames`の省略または空配列は現在の全cameraを意味する。APIは`Connected`なcameraだけを採用し、1台もなければ`409 NO_AVAILABLE_CAMERA`とする。成功応答は`202`とする。

```json
{
  "take":{"name":"take-1","phase":"Pending","cameraNames":["front"]},
  "excludedCameras":[{"name":"side","reason":"CAMERA_DISCONNECTED"}]
}
```

#### Take停止

```http
POST /api/v1/sessions/{sessionName}/takes/{takeName}/stop
{}
```

成功応答は`202`と`{"take":{"name":"take-1","phase":"Stopping"}}`。停止済みへの再送は現在状態を返して成功とする。

#### LiveKit参加token

```http
POST /api/v1/livekit/token
{}
```

requestごとに衝突しないparticipant identityを生成し、preview roomへjoinおよびsubscribeだけを許可する短時間（初期値5分）のtokenを返す。publish、data publish、room adminは許可しない。Web API自体の利用者認証は現行要件の範囲外とし、導入する場合もtoken権限は広げない。

```json
{"serverUrl":"wss://livekit.example.invalid","roomName":"kinugasa-preview","participantToken":"...","expiresAt":"2026-07-14T01:05:00Z"}
```

### 4.3 error形式

```json
{
  "error": {
    "code": "NAME_RESERVED",
    "message": "camera name has already been used",
    "details": {"field":"name","value":"front"},
    "requestId": "01J..."
  }
}
```

status codeはvalidation=`400`、未認証=`401`、権限不足=`403`、resource不在=`404`、状態または名前の競合=`409`、Kubernetes/LiveKit/S3の一時障害=`503`、予期しない障害=`500`とする。`code`は機械判定用の安定した文字列とし、少なくとも`INVALID_ARGUMENT`、`NOT_FOUND`、`NAME_RESERVED`、`TAKE_RECORDING`、`NO_AVAILABLE_CAMERA`、`STATE_CONFLICT`、`DEPENDENCY_UNAVAILABLE`、`INTERNAL`を定義する。

## 5. 映像入力、fan-out、preview

### 5.1 外部接続URLとport

- cameraごとにRIST用とSRT用のUDP NodePortを1つずつ割り当てる。
- 割当範囲はOperator設定`MEDIA_NODE_PORT_MIN`から`MEDIA_NODE_PORT_MAX`とし、既存Session CRに保存されたportを全namespace横断で予約済みとして扱う。
- スマートフォンへ返すhostは明示設定`PUBLIC_MEDIA_HOST`から生成する。HTTPのHost headerやPod/Service IPから推測しない。
- RIST受信はmain profile、SRT受信はlistener/live modeとする。クライアントURLはそれぞれ`rist://<host>:<port>`、`srt://<host>:<port>?mode=caller&transtype=live`とする。RIST profileは標準URL parameterではないためURLへ埋め込まず、camera client側でmain profileを選択する。FFmpegでは`-rist_profile main`相当をprotocol AVOptionとして明示する。
- 認証情報を将来URLへ追加する場合、QRコード表示用responseにだけ含め、CR statusやlogには残さない。

### 5.2 component間の経路

```text
smartphone -- RIST/SRT --> video-fanout
  video-fanout -- RTMP --> livekit-ingress -- WHIP --> LiveKit Ingress service --> LiveKit room
  video-fanout -- SRT --> video-recorder -- shared volume --> video-uploader -- S3
```

`video-fanout`は入力H.264を再encodeせずMPEG-TSとして内部の2つのloopback UDP branchへ複製する。preview branchは常駐processがRTMPへ転送する。録画branchはSRT listenerを常駐させ、take開始時に作られる`video-recorder`がcallerとして接続する。切断時にはGoのsupervisorがlistener processを再起動し、preview branchを止めない。内部SRTはcluster内Serviceだけに公開する。

`livekit-ingress`はcamera追加時にLiveKit Ingress APIでcamera固有のWHIP ingressを作成し、room名、participant identity、track名をcamera名へ対応付ける。RTMP listenerで受けたH.264をWHIPへ送る。camera削除時は先にWHIP送信を停止してIngressを削除し、participantがroomから消えたことを確認してからworkloadを削除する。

WHIP muxerはH.264のprofileやB frameに制約があるため、入力がWHIP互換ならstream copyし、互換でなければpreview branchだけをbaseline、zero-latency、B frameなしへ変換する。録画branchは常に入力H.264を再encodeしない。使用するimageはbuild testでRIST、SRT、RTMP、WHIP protocol/muxerが有効なことを確認する。

## 6. 録画fileとupload

### 6.1 volume上の契約

cameraごとのshared volumeを次の形にする。

```text
/recording/
├── staging/segment-00000000000000000000.ts.part
├── ready/segment-00000000000000000000.ts
├── state/recorder.json
└── state/recorder.done
```

- file番号はtake/cameraごとに0から始まる20桁の10進数とし、segment長は設定値（初期値10秒）とする。
- ffmpeg segment muxerへ`segment-<number>.ts.part`として書き、segment listにclose済みと記録されたfileだけを同一volume内の`ready/segment-<number>.ts`へatomic renameする。
- 正常停止はSIGINTで新規入力を止め、最後のsegmentのcloseとrename、`recorder.json`のatomic更新を行った後、最後に`recorder.done`をatomic作成する。
- crashで残った`.part`はupload対象にしない。recorderの異常としてstatusへ通知し、自動的に完成fileと推定しない。

### 6.2 upload

- uploaderは`ready/*.ts`だけを監視し、object keyを`<session>/<take>/<camera>/<file-name>`とする。
- fileごとにSHA-256を計算し、S3 object metadataへ保存する。同じkeyが既にありmetadataのdigestが一致すれば成功済み、異なれば上書きせず恒久的競合とする。
- upload成功記録はlocal stateへatomicに保存する。process再起動時はS3のHEADとdigestを照合して再開する。
- retry可能なnetwork error、timeout、5xx、rate limitには指数backoffとjitterを適用する。認証・bucket不在・digest競合等は恒久失敗として記録するが、設定変更後にreconcileで再試行できるようにする。
- `recorder.done`が存在し、`.part`がなく、発見した全ready fileがS3でdigest一致し、進行中uploadがない場合だけ当該cameraのuploadを完了とする。全cameraがこの条件を満たした時点でtakeを`Completed`にする。

## 7. 障害の伝達

Kubernetes標準のCondition形式を用い、`type`, `status`, `reason`, `message`, `observedGeneration`, `lastTransitionTime`を保持する。`message`は秘密情報を含めず人間向け、`reason`は機械判定可能な安定値とする。

主なConditionは次の通りとする。

| type | 主なreason | 意味 |
| --- | --- | --- |
| `Ready` | `ComponentsReady`, `ComponentsNotReady` | Session全体が操作可能か |
| `CameraConnected` | `FramesReceived`, `InputTimeout` | camera入力の接続状態 |
| `RecorderHealthy` | `ProcessRunning`, `ProcessExited` | 録画processの状態 |
| `UploadHealthy` | `Uploading`, `Retrying`, `PermanentFailure` | uploadの状態 |
| `UploadIncomplete` | `CompletionDeadlineExceeded` | 停止後もupload完了条件を満たさない |
| `LiveKitReady` | `IngressReady`, `IngressUnavailable` | preview経路の状態 |

componentはhealth/status endpointへprocess exit code、最終frame時刻、file数、upload数、最後のerror分類を公開する。reconcilerはこれとPod/Job状態をSession statusへ集約し、状態変化時にKubernetes Eventも発行する。Web UIはSession状態を定期取得し、将来watch/SSEへ置換可能な形にする。

警告を消す条件は原因の解消であり、単なる再取得では消さない。過去の詳細調査はKubernetes Eventとcomponent log、現在利用者へ示すべき状態はCR statusを正とする。

## 8. 実装時に固定する設定

次の値は要件ではなく運用設定であり、ConfigMap等から変更可能にする。

- `PUBLIC_MEDIA_HOST`、NodePort割当範囲
- preview room名、LiveKit公開URL、token TTL
- camera切断判定時間、録画segment長、upload完了警告期限
- S3 endpoint、region、bucket、path styleの使用有無
- upload retryのbackoff上限

LiveKit/S3のcredentialはSecretから取得し、API response、CR、Event、logへ出力しない。

## 9. 実装時に参照する一次資料

- [FFmpeg Protocols Documentation](https://ffmpeg.org/ffmpeg-protocols.html)（RIST、SRT、RTMPのURL option）
- [FFmpeg Formats Documentation](https://ffmpeg.org/ffmpeg-formats.html#whip)（WHIP muxerとH.264の制約）
- [LiveKit Ingress API](https://docs.livekit.io/reference/other/ingress/api/)（WHIP ingress、room、participantの設定）
- [LiveKit self-hosted Ingress](https://docs.livekit.io/transport/self-hosting/ingress/)（Ingress serviceとWHIP用port）
- [LiveKit Tokens & grants](https://docs.livekit.io/home/server/generating-tokens)（preview利用者tokenの権限）
