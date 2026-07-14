# Let's Note CF-SR実機確認票

KPI-001の受け入れと、実運用後の性能要件検討に必要な事実を同じ試行で記録する。
未記入の確認票を複製し、実施日を含むfile名で結果を保存する。

## 1. 環境

| 項目 | 記録値 |
| --- | --- |
| 実施日時・実施者 | |
| Let's Note型番 | CF-SR |
| CPU・memory | |
| OS・kernel | |
| Nix・Docker・k3d version | |
| 有線/無線LANと接続構成 | |
| S3互換service・region | |
| Camera端末の機種・OS・client | |
| Camera数 | |
| protocol | RIST main / SRT |
| 映像 | H.264、解像度: 、frame rate: |
| segment長・PVC容量 | |
| 連続運用時間 | |

versionと基礎情報の取得例:

```sh
uname -a
lscpu
free -h
nix --version
docker version
k3d version
kubectl version
```

## 2. 実施前確認

```sh
nix develop
make all
make k3d-create image-build k3d-import
PUBLIC_HOST=<LAN IPv4> make deploy
PUBLIC_HOST=<LAN IPv4> ./scripts/k3d-lan-check.sh
kubectl -n kinugasa-recording get deployments,pods
```

- [ ] S3/LiveKit credentialは開発用既定値ではない。
- [ ] host firewallで`docs/deployment.md`記載のportを同一LANから許可した。
- [ ] 全DeploymentがAvailable、全常駐PodがReadyである。
- [ ] Camera端末と操作browserからWeb UIへ到達できる。

## 3. 基本flow

詳細操作は`docs/operations.md`に従う。

- [ ] Web UIから未使用名でSessionを作成できる。（UC-006）
- [ ] 複数Cameraを追加し、各QRと同じRIST/SRT URLへ接続できる。（UC-001、KPI-006）
- [ ] 各Cameraのpreview映像と切断・再接続時の状態を確認できる。（UC-003）
- [ ] Camera未指定で全CameraのTakeを開始できる。（UC-002）
- [ ] 録画中にS3へ複数のMPEG-TS segmentが逐次追加される。（KPI-003）
- [ ] Take停止後に`Completed`となり、Cameraごとの全objectを取得・再生できる。（UC-005）
- [ ] Cameraを削除すると対応するworkloadが停止する。（UC-004）
- [ ] 使用済みSession/Camera/Take名は再利用できず、Web UIに警告される。

object確認例:

```sh
aws s3api list-objects-v2 --bucket "$S3_BUCKET" \
  --prefix "$SESSION/$TAKE/$CAMERA/"
aws s3 cp "s3://$S3_BUCKET/$SESSION/$TAKE/$CAMERA/" ./recording/ --recursive
ffprobe ./recording/segment-00000000000000000000.ts
```

## 4. 性能・安定性の記録

開始前、録画中の定常時、停止直後、upload完了後に次を保存する。

```sh
date --iso-8601=seconds
free -h
docker stats --no-stream k3d-kinugasa-recording-server-0
kubectl top node
kubectl -n kinugasa-recording top pods
kubectl -n kinugasa-recording get pods,jobs,pvc
kubectl -n kinugasa-recording get events --sort-by=.lastTimestamp
```

| 指標 | 開始前 | 録画中最大 | 停止直後 | upload完了後 |
| --- | ---: | ---: | ---: | ---: |
| host CPU使用率 | | | | |
| host memory使用量 | | | | |
| k3d node CPU使用率 | | | | |
| k3d node memory使用量 | | | | |
| operator CPU/memory | | | | |
| Camera別fanout CPU/memory | | | | |
| Camera別recorder CPU/memory | | | | |
| Camera別uploader CPU/memory | | | | |
| disk使用量 | | | | |
| S3 upload済みsegment数 | | | | |

追加記録:

- Camera clientが報告した送信frame drop:
- previewで観測した停止、遅延、破綻:
- 録画fileの欠落、再生不能、frame drop:
- Pod restart、OOM、disk pressure、その他のKubernetes Event:
- 操作上の問題:

## 5. 合否

KPI-001の初期合格条件は、指定した複数Camera・映像条件・運用時間を明記したうえで、
基本flowの全項目が成功し、録画objectの欠落・破損と処理異常がないことである。現時点では
性能閾値が要件化されていないため、CPU・memory・frame dropは合否値を後付けせず実測値を保存する。

| 判定 | 記録 |
| --- | --- |
| 基本flow | 合格 / 不合格 |
| 録画objectの完全性 | 合格 / 不合格 |
| 処理異常 | なし / あり（内容: ） |
| KPI-001総合 | 合格 / 不合格 |
| 未解決事項・再試験条件 | |
