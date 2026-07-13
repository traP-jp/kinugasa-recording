# kinugasa-recording TODO

`docs/requirements.md`をSSoTとし、`docs/codebase.md`で定めた責務の境界に沿って実装する。
主要機能とcontainer/Kubernetes構成まで完了し、LAN上の実機確認、test拡充、運用文書を進める段階である。

このファイルには未完了のタスクだけを記載する。完了したタスクはチェック済みの状態で残さず、項目自体を削除する。

## 0. 要件の確認と設計

### 実装前に文書化する事項

- [ ] 実装で確定したpackage・fileの配置を、各phaseの完了時に`docs/codebase.md`へ反映する。

## 8. Container imageとKubernetes環境

- [ ] スマートフォンから映像入力用Serviceへ、利用者のbrowserからWeb UIとLiveKitへLAN内で到達できるようにする。

## 9. Testと受け入れ確認

### Unit test

- [ ] Session、camera、takeの状態遷移とreconcileの冪等性をtestする。
- [ ] S3 object key、逐次upload、retry、upload完了判定をtestする。

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
