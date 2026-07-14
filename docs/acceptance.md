# 初期実装の受け入れ状況

`docs/requirements.md`を基準に、2026-07-14時点の自動testと残る実機確認をまとめる。

## KPI

| 要件 | 状況 | 根拠 |
| --- | --- | --- |
| KPI-001 | 未確認 | Let's Note CF-SR実機上の基本flowと性能計測が必要 |
| KPI-002 | 確認済み | React Web UIのunit testと公開Web APIを通るE2E |
| KPI-003 | 確認済み | 録画中の複数MPEG-TS segment逐次uploadと停止後の全object照合 |
| KPI-004 | 確認済み | RIST main profile・SRTの実映像integration test |
| KPI-005-1 | 確認済み | 2 Camera一括Takeと1 Camera選択TakeのE2E |
| KPI-005-2 | 確認済み | LiveKit ingress接続・参加tokenとWeb preview test |
| KPI-005-3 | 確認済み | Session/Take/Camera命名規則と使用済み名拒否のunit/E2E |
| KPI-005-4 | 確認済み | Camera追加・削除と子resource cleanupのintegration/E2E |
| KPI-006 | 確認済み | API接続URLとQR値のunit test、QRと同じLAN SRT URLへのE2E入力 |

## 機能要件とユースケース

| 要件 | 対応する確認 |
| --- | --- |
| FR-001 / UC-001 | Camera API、Session CR更新、fanout/LiveKit ingress作成、接続・preview、重複警告 |
| FR-002 / UC-002 | Take API/CR、camera別recorder/uploader/PVC、分割録画、逐次upload、停止・完了、障害警告 |
| FR-003 / UC-003 | LiveKit room初期化、camera ingress、browser参加token、切断警告 |
| FR-004 / UC-004 | Camera削除、LiveKit ingress削除、Deployment/Service/Secret停止・cleanup |
| FR-005 / UC-005 | `<Session>/<Take>/<Camera>/segment-*.ts`の全件照合、MPEG-TSとして全object取得 |
| FR-006 / UC-006 | S3とCR双方の使用履歴を確認するSession作成、重複警告 |

自動testの実行方法と範囲は`test/README.md`、実機手順は`docs/operations.md`を参照する。
初期実装の最終受け入れには、KPI-001の実機確認を完了する必要がある。
