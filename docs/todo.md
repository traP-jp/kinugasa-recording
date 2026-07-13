# kinugasa-recording TODO

`docs/requirements.md`をSSoTとし、`docs/codebase.md`で定めた責務の境界に沿って実装する。
設計とRepository基盤が完了し、API・Custom Resource・Operator基盤の実装に着手する段階である。

このファイルには未完了のタスクだけを記載する。完了したタスクはチェック済みの状態で残さず、項目自体を削除する。

## 0. 要件の確認と設計

### 実装前に文書化する事項

- [ ] 実装で確定したpackage・fileの配置を、各phaseの完了時に`docs/codebase.md`へ反映する。

## 2. API・Custom Resource・Operator基盤

- [ ] Session Custom ResourceのGo型、CRD、validation、status subresourceを実装する。cameraとtakeの定義・状態・使用済み名称をSessionに関連付ける。
- [ ] Operatorに必要なServiceAccount、RBAC、CRD manifestを`config/`へ追加する。
- [ ] Sessionを監視してworkloadのdesired stateを反映するreconcilerの基盤を実装する。
- [ ] Web UI向けHTTP server、routing、validation、共通error response、Custom Resourceの状態取得を実装する。
- [ ] Kubernetes API、LiveKit、S3、子processに対する一時的な失敗をreconcile可能にし、statusへ反映する。

## 3. Session作成

- [ ] session名の形式をvalidationする。
- [ ] 同名のSession Custom Resourceが現在または過去に存在しないことを確認する。
- [ ] S3互換object storageに同名sessionのobject prefixが現在または過去に存在しないことを確認する。
- [ ] 条件を満たす場合にSession Custom Resourceを作成し、重複時には競合を表すHTTP errorを返すAPIを実装する。（UC-006、FR-006）
- [ ] Web UIにsession名の入力、作成操作、validation・重複警告、作成後のsession画面への遷移を実装する。

## 4. 映像処理コンポーネント

### video-fanout

- [ ] H.264映像をRIST main profileで受信し、録画系とpreview系へ分岐するffmpeg process制御を実装する。
- [ ] H.264映像をSRTで受信し、録画系とpreview系へ分岐するffmpeg process制御を実装する。
- [ ] preview系の出力をRTMPで`livekit-ingress`へ転送する。
- [ ] processの起動・終了、signal処理、異常終了、接続状態を外部から観測できるようにする。

### livekit-ingress

- [ ] RTMP映像を受信し、WHIPでLiveKitへ中継するffmpeg process制御を実装する。
- [ ] cameraごとのLiveKit participant/trackを作成・削除できるようにする。
- [ ] processの起動・終了、signal処理、異常終了を外部から観測できるようにする。

### video-recorder

- [ ] fan-outされたH.264映像を再encodeせずMPEG-TSコンテナとしてshared volumeへ保存する。（FR-002-4）
- [ ] 録画開始・停止、file確定、異常終了を`video-uploader`から安全に判別できるようにする。

### video-uploader

- [ ] 確定した録画fileを録画中から検出し、S3互換object storageへ逐次uploadする。（FR-002-3）
- [ ] object keyを`<session>/<take>/<camera>/<video-file>`とする。（FR-005）
- [ ] uploadのretry、重複実行時の冪等性、失敗状態、全fileのupload完了判定を実装する。
- [ ] endpoint、bucket、認証情報をConfigMap・環境変数・Secretから受け取る。

## 5. LiveKitとpreview

- [ ] cluster起動時にpreview用LiveKit roomを1つ初期化する。（FR-003-1）
- [ ] Web UIからのrequestに対して、必要最小限の権限と有効期限を持つLiveKit参加tokenを発行する。（FR-003-2）
- [ ] Web UIにLiveKit roomへの接続とcameraごとのリアルタイムpreviewを実装する。（KPI-005-2、UC-003）
- [ ] cameraが未接続または切断された場合、Web UIに警告を表示する。（UC-003例外フロー）

## 6. Cameraの追加・削除

### 追加

- [ ] camera名の形式、session内での現在・過去を通した一意性、take録画中でないことを検証する。
- [ ] camera追加APIでSession Custom Resourceを更新する。（FR-001-1）
- [ ] cameraごとの`video-fanout`と`livekit-ingress` workload、Service、portをreconcileする。（FR-001-2）
- [ ] `livekit-ingress`起動後にcamera入力をLiveKit roomへ追加する。（FR-001-3）
- [ ] RIST用とSRT用の接続先URLをAPIで返し、Web UIに文字列とQRコードで表示する。（FR-001-4、KPI-006）
- [ ] Web UIにcamera追加、重複名称の警告、接続状態、previewを実装し、接続完了まで確認できるようにする。（UC-001）

### 削除

- [ ] camera削除APIでSession Custom Resourceを更新し、名称の使用履歴を保持する。（FR-004-1）
- [ ] `livekit-ingress`停止前にcamera入力をLiveKit roomから削除する。（FR-004-3）
- [ ] 対応する`video-fanout`、`livekit-ingress`、Serviceを停止・削除する。（FR-004-2）
- [ ] Web UIにcamera削除操作を実装し、関連workloadの停止完了を表示する。（UC-004）

## 7. Takeの録画とupload

- [ ] take名の形式、session内での現在・過去を通した一意性を検証する。
- [ ] 使用するcameraの選択を受け付け、未指定時は全cameraを選択する。利用不能なcameraは開始対象から除外し、結果を利用者へ示す。（UC-002）
- [ ] take開始APIでSession Custom Resourceのdesired stateを更新する。（FR-002-1）
- [ ] cameraごとの`video-recorder`、`video-uploader`、shared volumeを持つworkloadをreconcileし、録画を開始する。（FR-002-2）
- [ ] take停止APIでSession Custom Resourceを更新し、各recorderを停止してから全uploadの完了を待つ。（FR-002-1）
- [ ] upload完了後に録画用workloadとvolumeを安全にcleanupし、takeを完了状態にする。
- [ ] 録画中のcamera切断をWeb UIに警告し、takeは自動停止しない。（UC-002例外フロー3a）
- [ ] 録画中のupload失敗をWeb UIに警告し、takeは自動停止しない。（UC-002例外フロー3b）
- [ ] 停止後にuploadが完了しない場合、Web UIに警告とcameraごとの状態を表示する。（UC-002例外フロー5a）
- [ ] UC-001とUC-004の操作をtake録画中は拒否し、Web UI上でも無効化する。

## 8. Container imageとKubernetes環境

- [ ] `operator`、`video-fanout`、`video-recorder`、`video-uploader`、`livekit-ingress`、Web UIをそれぞれDocker imageとしてbuildできるようにする。
- [ ] 各imageに必要なffmpeg機能（RIST、SRT、RTMP、WHIP、H.264、MPEG-TS）が含まれることをbuild時またはtestで確認する。
- [ ] Operator、Web UI、LiveKit、各種設定をdeployするKustomize baseを`config/`に作成する。
- [ ] k3d向けoverlayと実運用向けoverlayを分離する。
- [ ] S3 endpoint・bucket等をConfigMap、認証情報をSecretで注入するmanifestを用意する。
- [ ] k3d clusterの作成、image import、deploy、破棄を行うscriptを`scripts/`に用意する。
- [ ] スマートフォンから映像入力用Serviceへ、利用者のbrowserからWeb UIとLiveKitへLAN内で到達できるようにする。

## 9. Testと受け入れ確認

### Unit test

- [ ] 名称validationと、現在・過去を通した重複拒否をtestする。
- [ ] Session、camera、takeの状態遷移とreconcileの冪等性をtestする。
- [ ] ffmpeg command生成、process終了処理、録画file確定判定をtestする。
- [ ] S3 object key、逐次upload、retry、upload完了判定をtestする。
- [ ] Web UIの主要な操作とerror・警告表示をtestする。

### Integration test

- [ ] k3d上でSession Custom Resource、Operator、各workloadの作成・更新・削除をtestする。
- [ ] RIST main profileとSRTの両方についてH.264入力を受信し、LiveKit previewと録画へ分岐できることをtestする。
- [ ] 録画結果がMPEG-TSであり、`<session>/<take>/<camera>/`以下へ録画中からuploadされることをtestする。
- [ ] camera切断、ffmpeg異常終了、S3一時障害・恒久障害を発生させ、status、retry、Web UIへの警告をtestする。

### End-to-end test

- [ ] UC-006、UC-001、UC-003、UC-002、UC-004の基本フローを順に実行するtestを作成する。
- [ ] 複数cameraの一括録画、camera選択、未指定時の全選択をtestする。（KPI-005-1）
- [ ] session、camera、takeの不正名・重複名・使用済み名をWeb UIから指定し、拒否と警告をtestする。（KPI-005-3）
- [ ] QRコードの内容が選択したprotocolの到達可能な接続先URLと一致することをtestする。（KPI-006）
- [ ] 録画停止後、すべてのuploadが完了してS3から取得できることをtestする。（UC-005）
- [ ] Let's Note CF-SR上でsession作成、複数cameraのpreview、take録画、逐次uploadの基本フローが動作することを確認する。（KPI-001）

## 10. 運用文書と完了条件

- [ ] 開発環境の構築、build、test、k3dへのdeploy、映像送信、録画、S3確認の手順を文書化する。
- [ ] RIST/SRTの接続先、必要port、Kubernetes・LiveKit・S3の設定項目とSecret作成手順を文書化する。
- [ ] 障害時の確認箇所、録画・uploadの再試行または復旧方法を文書化する。
- [ ] 実際の配置と責務に合わせて`docs/codebase.md`を最終更新する。
- [ ] KPI-001からKPI-006、FR-001からFR-006、UC-001からUC-006の対応状況を確認し、すべての受け入れtestが成功したら初期実装を完了とする。

## 11. 実運用後の性能検討

- [ ] Let's Note CF-SRでの実運用時に、機種・OS、同時camera数、解像度、frame rate、運用時間と、CPU・memory使用量、frame drop、処理異常を記録する。
- [ ] 実運用時の計測結果に基づいて必要な性能と受け入れ条件を検討し、`docs/requirements.md`へ反映する。（KPI-001）
