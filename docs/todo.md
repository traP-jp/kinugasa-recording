# kinugasa-recording TODO

`docs/requirements.md`をSSoTとし、`docs/codebase.md`で定めた責務の境界に沿って実装する。
主要機能、container/Kubernetes構成、自動test、運用文書は完了している。
残る作業はLet's Note CF-SR上の実機確認と、その実測に基づく性能要件の検討である。

このファイルには未完了のタスクだけを記載する。完了したタスクはチェック済みの状態で残さず、項目自体を削除する。

## 9. Testと受け入れ確認

### End-to-end test

- [ ] Let's Note CF-SR上でsession作成、複数cameraのpreview、take録画、逐次uploadの基本フローが動作することを確認する。（KPI-001）

## 10. 運用文書と完了条件

- [ ] KPI-001からKPI-006、FR-001からFR-006、UC-001からUC-006の対応状況を確認し、すべての受け入れtestが成功したら初期実装を完了とする。

## 11. 実運用後の性能検討

- [ ] Let's Note CF-SRでの実運用時に、機種・OS、同時camera数、解像度、frame rate、運用時間と、CPU・memory使用量、frame drop、処理異常を記録する。
- [ ] 実運用時の計測結果に基づいて必要な性能と受け入れ条件を検討し、`docs/requirements.md`へ反映する。（KPI-001）
