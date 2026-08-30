# kinugasa-recording v2：実装前に不足している情報

## 調査範囲と判定基準

`docs/requirements-v2/` 配下の全23文書に加え、`contracts/console-api/`のOpenAPI定義と`contracts/lockfile/lockfile.schema.json`を確認した。ここでは、実装方式を一意に決められない、コンポーネント間で契約を共有できない、または受け入れ判定を作れない事項を「不足」とした。単なる内部実装の自由度（アルゴリズムやコード構成）は対象外である。

優先度は次の意味で使用する。

- **P0（着手前に必須）**: API・データ・コンポーネント間の互換性や主要な正常系を決める情報。
- **P1（結合前に必須）**: 障害時動作、運用、セキュリティ、品質保証を決める情報。
- **P2（リリース判定前に必須）**: 非機能要件や検証条件を決める情報。

なお、本書はDraftであり（`requirements-v2/02-front-matter/front-matter.md`）、定義、参照文献、略語、製品機能、ユーザー特性、制限事項、前提条件・依存関係、標準準拠、システム属性、補足情報の各章は見出しだけで本文がない。以下には、それらの空欄によって実際に決められない事項も含めた。

## P0：実装着手前に決定が必要

### 1. サービス間インターフェース

video gatewayからcameraごとのvideo workerへのRTP中継、LiveKitへの中継、uploaderによるアップロードという役割だけが記述されている（`requirements-v2/08-product-perspective/product-perspective.md`）。次の契約が必要である。

- console serverがgateway、各worker、uploaderへ作成・開始・停止を指示する方法
- 各workerが接続状態、拒否理由、開始・終了時刻、進捗、エラーを通知する方法とpayload
- video workerがUUIDを「通知」する先、対応するCameraConnectionを特定する方法、プロトコル、再送・重複時の扱い
- RTPのtransport（UDP/TCP）、宛先割当、payload type、SSRC、clock rate、RTCPの有無、音声payload、timestampの基準
- gateway/worker間のreadiness、timeout、heartbeat、graceful shutdown契約、およびworkerが確定した録画ファイルをuploaderへ通知する方法
- LiveKitのroom、participant、trackとSession/CameraIdentityの対応、およびpublish方式
- console server停止中にもworkerが継続するために、命令と状態をどこへ永続化し、再接続時にどう再同期するか

### 2. camera入力プロトコル

RIST Main Profile、H.264、30 fps、およびvideo gatewayに`ristreceiver`を使うことまでは指定されている（`requirements-v2/14-specified-requirements/01-external-interfaces/external-interfaces.md`、`requirements-v2/14-specified-requirements/06-design-constraints/design-constraints.md`）が、受信可能なbitstreamを確定できない。

- RISTのURL形式、listen/connect mode、具体的なMain Profile設定
- RIST上のcontainer/encapsulationとprogram/stream選択規則
- H.264 profile/level、解像度、pixel format、bitrate、GOP/B-frame、VFR可否
- 「30 fps」の許容差、判定に使う期間、接続後いつまでに拒否するか
- 音声codec、sample rate、channel数、音声が途中で出入りする場合の扱い
- 複数映像stream、データstream、不正packet、timestamp欠落・巻き戻りの扱い
- URLのscheme、host、port、query、外部から到達可能なhostを生成する規則
- 同じcameraからの二重接続、再接続時の旧接続、および再接続backoffの扱い

### 3. 録画成果物の残る仕様

成果物の論理パス、object key、SHA-256、size、`.mp4`という拡張子は定義されている（`requirements-v2/14-specified-requirements/01-external-interfaces/external-interfaces.md`、`contracts/lockfile/lockfile.schema.json`）が、次が不足している。

- MP4のprofile（通常MP4またはfragmented MP4）、映像・音声codecをcopyするかtranscodeするか、time base
- moov atom、trailer、各trackの必須metadataなど、正常な成果物と判定する条件
- objectへ付与するcontent type、metadata/tag、および既存objectとの衝突時の扱い
- camera切断、worker crash、停止timeoutなどで生じた部分ファイルを保存するか破棄するか
- shared volumeの容量予約、容量不足時の動作、upload完了後の録画ファイルとvolumeの具体的な削除手順

### 4. 録画開始・停止と同期方式

「数秒オーダーの誤差を許容した同期」（`requirements-v2/07-scope/scope.md`）とdrift目標はあるが、実装可能な正常系の定義がない。

- 同期の基準となるclock（camera timestamp、RTP timestamp、各workerのmonotonic clock、wall clockなど）
- `startedAt`/`finishedAt`をどのコンポーネントのどのevent時刻とするか
- take開始要求後、各cameraが実際に録画を始める条件とtimeout
- cameraごとの開始・終了差の許容値。「数秒オーダー」の具体的上限
- 遅れて接続・再接続したcameraを同じtakeへ復帰させるか
- stop時のflush、trailer確定、最終frame、worker acknowledgementの順序
- 入力jitterを吸収するbuffer、timestamp補正、frame重複・欠落の基本方針
- 音声と映像、およびcamera間の同期要件

### 5. ドメイン状態遷移の未定義部分

エンティティと一部の遷移は定義されている（`requirements-v2/14-specified-requirements/05-logical-database-requirements/logical-database-requirements.md`）が、次が欠けている。

- Sessionの初期state、`active`/`inactive`の意味、遷移trigger、worker起動・停止との関係。stateを変更するAPIもない
- CameraConnectionの作成・削除に対してvideo workerを起動・停止する正確な条件
- CameraConnectionの各status間の許可遷移と、`error`から再接続成功した際の遷移
- camera追加・削除中のKubernetes失敗、Service割当timeout、gateway crash時の状態
- OngoingTake作成時、指定されたcameraをRecordingCameraへsnapshotする時点と順序
- RecordingCameraが全て`errored`になった場合もOngoingTakeを継続するか
- 正常停止とエラー停止それぞれでRecordingCameraをVideoFileへ変換する規則
- worker crashでerroredになったRecordingCameraにVideoFileを作るか、部分録画をuploadするか
- uploaderのretry中、永久失敗、process crash、再起動後の状態遷移
- FinishedTake/VideoFileの状態更新を原子的に行う規則
- Session、FinishedTake、CameraIdentityを削除・保持する期間。Session削除APIは存在しない

### 6. データモデルとschema間の不整合

以下は実装方針の選択では解消できず、要求の修正または明文化が必要である。

- RecordingCameraの`cameraIdentityId`は型と関係図ではCameraIdentityを指すが、属性説明ではCameraConnectionへの参照とされている
- 正常終了時にVideoFileを作るとされるが、erroredなRecordingCameraに対応するVideoFileの有無が不明
- 論理モデルのVideoFileには`size`があるが、`contracts/console-api/schemas/take.yaml`のVideoFileには`size`がない
- `FinishedTake=uploading` iff 1件以上のVideoFileがuploadingという制約では、VideoFile作成前の非同期区間や、全件を同一transactionでcompleted/erroredへ更新できない場合の中間状態を表現できない
- Timestampのtimezone・精度・丸めがない
- DB製品、schema/migration方式、外部キー、unique制約、transaction isolationがない

### 7. Kubernetesリソース設計の外部契約

Kubernetes Operatorであることは決まっているが、reconcile対象と生成物の契約がない。

- DBを正とするoperatorがwatch/reconcileする対象。CRDを使用するか、使用するならschemaとDBとの正の所在
- Session/camera/takeごとに生成するDeployment/Pod/Job/Service等と所有関係。video worker Podごとに1つのPVCを事前作成し、workerとuploaderを同じPod内の別containerにすることは決まっている
- gatewayとcameraごとのworkerの配置単位、namespace、resource naming、labels/annotations、owner references
- Service type、port割当、外部IP/hostの取得方法、割当失敗・変更時のURL更新
- image、command/args、設定値、Secret/ConfigMap、resource request/limit、security context
- rollout/restart policy、readiness/liveness/startup probe、grace period、Job完了・回収条件
- 複数console server replicaを許すか。許す場合のleader electionと同時reconcile

## P1：結合・運用前に決定が必要

### 8. オブジェクトストレージ契約とupload失敗処理

- 対応API（S3互換か否か）、endpoint/region、path-style、TLS、credential供給方法
- multipart uploadの閾値、並列数、timeout、retry/backoff、再起動時のresume/abort
- 同じobject keyへの再実行の冪等性と、既存objectが異なる場合の扱い
- upload成功の定義、hash照合、整合性確認
- 一時障害と永久障害の分類、最大試行回数、手動retry APIの要否
- completed/errored後のローカルファイルと未完multipart uploadのcleanup

### 9. 外部アクセスとsecret管理

- CSRF/CORS方針と、browserからconsole APIへ到達させる構成
- camera URLを知る第三者の接続を許すか、cameraごとのcredentialとrotation/revoke
- LiveKit短期tokenのTTL、subject、room/track権限、一度きりか、更新方法
- Kubernetes Secretの生成・配布・rotation、ログやUIでのsecret masking
- 外部公開するService/APIのnetwork boundaryとNetworkPolicy

### 10. 障害・再起動・reconciliationの詳細

DBを正として復元する方針はある（`requirements-v2/14-specified-requirements/02-functions/functions.md`）が、収束条件が不足している。

- desired stateとobserved stateの項目、および各リソースのreconcile結果をDBへ反映する規則
- Kubernetes API、DB、LiveKit、object storageが停止した際のtimeout/retry/backoff
- console server再起動中に進んだ録画・upload eventの欠落防止、順序逆転、重複排除
- worker停止を「予期しない」と判定する条件。意図したrollout/node drain/OOM/evictionの区別と、対応するcameraの状態への反映
- gateway/uploader/LiveKit停止時のドメイン状態と自動復旧範囲
- DB更新とKubernetes操作の間でcrashした場合の補償処理
- 孤児Pod/Service/PVC/object、古いvideoWorkerId、割当変更後の古いURLのcleanup
- 復旧不能時の手動操作（retry/cancel/force-finish）が必要か。そのAPI/UI

### 11. web consoleの操作仕様

ページと大まかな役割（`requirements-v2/14-specified-requirements/03-usability-requirements/usability-requirements.md`）だけではUI状態を実装できない。

- 対象browser/device、画面幅、同時利用者、言語・timezone
- API状態更新をpolling/SSE/WebSocketのどれで取得するか、更新間隔、切断時表示
- 各フォームのvalidation、送信中・成功・失敗表示、重複操作防止
- camera削除/take停止の確認、操作不能条件と理由表示
- 全cameraか選択cameraかという録画開始UI、および未接続cameraの表示・選択規則
- previewの自動再生、音声、mute、track再接続、表示上限、LiveKit tokenの更新方法
- QRコードの規格・誤り訂正level・併記文字列・copy操作
- FinishedTake/VideoFileのerror表示内容、upload進捗の定義
- accessibility（keyboard、contrast、label等）とブラウザ互換性の合格基準

### 12. 観測性と運用契約

- componentごとのstructured log項目、level、correlation ID、個人情報/secretの扱い
- metrics（接続、bitrate、jitter、frame loss、録画容量、upload進捗、reconcile失敗）とalert条件
- health/readinessの意味、運用者が見るstatus、診断情報の取得方法
- audit logの要否（誰がsession/camera/takeを操作したか）
- 容量監視、disk枯渇、DB肥大、object storage quota時の動作
- backup/restore、災害復旧、upgrade/migration/rollback手順と許容停止時間

### 13. 依存コンポーネントと実行環境

主要な実装言語とMediaMTX、`ristreceiver`の採用は決まっているが、再現可能なbuild/deploy条件がない。

- 対応Kubernetes distribution/version、OS/CPU architecture、container runtime
- Go、Node.js、TypeScript、React、MediaMTX、librist、LiveKit、DB、object-storage SDKのversion
- cameraクライアントとして保証するMoblin/iOS/iPadOS version（検証章では記録するだけ）
- 必要なCPU/GPU命令、hardware accelerationの有無、永続volumeのaccess mode/filesystem
- network前提（同一LAN、NAT、MTU、帯域、IPv4/IPv6、DNS、clock synchronization/NTP）
- dev/test/productionで外部依存をどう用意するか、および設定parameter一覧

## P2：性能・受け入れ判定前に決定が必要

### 14. 容量・性能・可用性の数値

現状の性能要求はcamera 2台・10分・driftの努力目標だけである（`requirements-v2/14-specified-requirements/04-performance-requirements/performance-requirements.md`）。次が未定義である。

- 1 Sessionあたりおよびsystem全体の最大camera数、Session数、同時録画数
- 対応解像度・bitrate、必要network帯域、10分を超える最大take時間、最大ファイル容量
- REST応答、camera接続準備、録画開始/停止、preview表示、upload完了のlatency目標
- previewの許容遅延・frame rate・画質
- packet loss、jitter、帯域変動、切断時間の許容範囲
- frame落ち、映像破損、A/Vずれ、開始・終了時刻差の合否閾値
- CPU/memory/disk/network使用量の上限と、基準hardware
- 可用性、復旧時間、データ損失許容、録画中のnode障害を対象にするか

### 15. 検証条件と要求トレーサビリティ

drift検証には明示的なTBDが残っている（`requirements-v2/14-specified-requirements/09-verification/verification.md`）。また、ほかの機能の受け入れ方法がない。

- network jitterの分布、範囲、相関、投入tool/設定、再現用seed
- drift推定の解析区間、前処理、信頼区間、試行回数、集約方法と合否判定
- frame落ち検出時の30 fpsに対する許容差、PTS不連続・wrap・VFRの扱い
- camera拒否、切断、worker crash、console再起動、upload失敗・再開を検証する具体的scenario
- API schema/contract test、DB不変条件、reconciliation、UI/browser、securityの受け入れ基準
- test用端末・network・Kubernetes・LiveKit・object storageの固定version/構成
- 各要求を一意に参照するrequirement IDと、その要求を証明するtestへの対応表

## 仕様間で優先的に回答すべき質問

実装の手戻りを避けるため、少なくとも次の順で意思決定が必要である。

1. camera入力のwire formatと、gateway―worker間RTP契約は何か。
2. console serverと各workerのcommand/event protocol、および停止中の状態同期方法は何か。
3. MP4の生成方式と、切断・crash時の部分ファイルをどう扱うか。
4. take開始時の非接続camera、途中切断・再接続、および全camera失敗をどう扱うか。
5. Session、CameraConnection、Take、VideoFileの全状態遷移とtransaction境界は何か。
6. Kubernetes上に作るresource、その所有関係、Service公開方式は何か。
7. DB、LiveKit、object storage、Kubernetes、MediaMTX、libristの対応versionと設定は何か。
8. camera credential、LiveKit token、Kubernetes Secretおよび外部公開するService/APIの管理方式は何か。
9. 想定最大camera数・解像度・bitrate・録画時間と、受け入れ可能な同期差・frame落ち・復旧時間は何か。

この9点が決まるまでは、型やコンポーネント境界を仮定したprototypeは可能でも、相互運用可能な本実装とその完了判定は確定できない。
