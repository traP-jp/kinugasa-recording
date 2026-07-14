# 開発・運用ガイド

## 1. 前提

- x86_64 Linux、Nix flakes、Docker daemonを使用する。
- camera client、host PC、操作用browserは同じLANへ接続する。
- S3互換bucketを事前に作成する。bucket管理は本システムの対象外である。
- 名前は英数字とハイフンだけを使用する。Session名はbucket内、Camera名とTake名はSession内で再利用できない。

開発toolchainへ入り、依存packageを取得する。

```sh
nix develop
pnpm install --frozen-lockfile
```

通常の検証は`make all`でformat、lint、unit test、buildをまとめて実行する。

```sh
make all
```

## 2. S3とLiveKitの設定

`config/default/platform-config.yaml`の次の値を環境に合わせる。継続運用する差分は
`config/overlays/production`などのKustomize overlayで管理し、defaultの開発値を直接使わない。

| ConfigMap | key | 意味 |
| --- | --- | --- |
| `kinugasa-recording-s3` | `S3_BUCKET` | 作成済みbucket名 |
| 同上 | `S3_REGION` | region |
| 同上 | `S3_ENDPOINT` | AWS S3では空、S3互換serviceではAPI endpoint |
| 同上 | `S3_USE_PATH_STYLE` | endpointがpath-styleを必要とするとき`true` |
| `kinugasa-recording-operator` | `PUBLIC_MEDIA_HOST` | cameraから到達できるhostのLAN IPv4。deploy scriptが設定する |
| 同上 | `MEDIA_NODE_PORT_MIN/MAX` | cameraへ割り当てるUDP NodePort範囲。既定は`31000-31099` |
| 同上 | `LIVEKIT_PUBLIC_URL` | browserから到達できるWebSocket URL。deploy scriptが設定する |
| 同上 | `LIVEKIT_ROOM` | preview room名 |
| 同上 | `LIVEKIT_TOKEN_TTL` | Web UIへ発行する参加tokenの寿命 |
| 同上 | `RECORDING_VOLUME_SIZE` | Take・Cameraごとの一時録画PVC容量 |

S3 credentialは`kinugasa-recording-s3` Secretの`AWS_ACCESS_KEY_ID`と
`AWS_SECRET_ACCESS_KEY`へ設定する。LiveKitは`kinugasa-recording-livekit` Secret内の
`api-key`、`api-secret`、`keys`、`ingress.yaml`で同じkey/secretを使う。開発用の
`REPLACE_ME`や既定LiveKit secretを実運用で使用しない。credentialを含むmanifestはcommitせず、
Secret managerまたは暗号化したoverlayから適用する。

既存SecretをCLIから更新する場合の例を示す。値はshell historyへ直接記録しない。

```sh
read -r AWS_ACCESS_KEY_ID
read -r -s AWS_SECRET_ACCESS_KEY
export AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY
kubectl -n kinugasa-recording create secret generic kinugasa-recording-s3 \
  --from-literal=AWS_ACCESS_KEY_ID="$AWS_ACCESS_KEY_ID" \
  --from-literal=AWS_SECRET_ACCESS_KEY="$AWS_SECRET_ACCESS_KEY" \
  --dry-run=client -o yaml | kubectl apply -f -
unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY
```

## 3. k3dへのdeploy

初回はcluster作成、全imageのbuild/import、deployを行う。

```sh
make k3d-create image-build k3d-import
make deploy
./scripts/k3d-lan-check.sh
```

`make deploy`はdefault routeからLAN IPv4を検出する。複数NICやVPNを使う場合は明示する。

```sh
PUBLIC_HOST=192.168.1.20 make deploy
PUBLIC_HOST=192.168.1.20 ./scripts/k3d-lan-check.sh
```

変更後は`make image-build k3d-import deploy`を実行する。clusterを削除するときは
`make k3d-destroy`を使う。公開portとfirewallの詳細は`docs/deployment.md`を参照する。

## 4. camera入力、preview、録画

1. `http://<LAN IPv4>:30080`を開き、Sessionを作成する。
2. Camera名を入力して追加する。
3. Web UIが表示するRISTまたはSRT QRをcamera clientで読み取る。RIST clientはmain profile、
   SRT clientはcaller/live mode、映像codecはH.264を使用する。
4. previewと接続状態を確認する。
5. Take名と対象Cameraを選んで開始する。未選択なら接続済みの全Cameraが対象になる。
6. 書き込み済みsegmentから録画中にもuploadされる。停止後、Takeが`Completed`になるまで待つ。

PCからSRT入力を試す例は次の通り。URLは画面に表示された値をそのまま使う。

```sh
ffmpeg -re -i input.mp4 -an -c:v libx264 -preset ultrafast -tune zerolatency \
  -profile:v baseline -pix_fmt yuv420p -f mpegts \
  'srt://192.168.1.20:31001?mode=caller&transtype=live'
```

RISTでは画面の`rist://<host>:<port>`へ送信し、client側でmain profileを選ぶ。

録画object keyは`<Session>/<Take>/<Camera>/segment-<20桁番号>.ts`である。AWS CLI互換の例:

```sh
aws s3api list-objects-v2 --bucket "$S3_BUCKET" \
  --prefix "$SESSION/$TAKE/$CAMERA/"
aws s3 cp "s3://$S3_BUCKET/$SESSION/$TAKE/$CAMERA/" ./recording/ --recursive
```

S3互換serviceでは必要に応じて両commandへ`--endpoint-url "$S3_ENDPOINT"`を付ける。

## 5. test

deploy済みclusterに対するtestは次を使用する。

```sh
make test-integration
make test-e2e
```

integration testはworkload lifecycle、RIST/SRT fanout、録画・逐次upload、一時障害からの
retry、恒久障害の通知を確認する。E2Eは公開Web APIから2 Cameraの基本flowとS3上の全録画
object取得までを確認する。test用S3 mockへ一時的に切り替え、終了時に元の設定へ戻す。

## 6. 障害調査と復旧

まずWeb UIの警告とSession statusを確認し、次にKubernetes resource、Event、logを調べる。

```sh
kubectl -n kinugasa-recording get krsessions
kubectl -n kinugasa-recording get krsession <resource-name> -o yaml
kubectl -n kinugasa-recording get deployments,pods,jobs,pvc
kubectl -n kinugasa-recording get events --sort-by=.lastTimestamp
kubectl -n kinugasa-recording logs deployment/operator --since=15m
kubectl -n kinugasa-recording logs <pod-name> --all-containers
```

| 症状 | 確認箇所 | 対応 |
| --- | --- | --- |
| Web/previewへ接続できない | `k3d-lan-check.sh`、firewall、LiveKit/Ingress Pod、`PUBLIC_MEDIA_HOST` | 同一LANと公開IPv4を確認し、`PUBLIC_HOST=... make deploy`を再実行する |
| Cameraが`Disconnected` | Camera client、UDP NodePort、fanout Pod log、Sessionの`CameraConnected` | 入力を再接続する。fanout内processは異常終了時に自動再起動し、Takeは自動停止しない |
| Recorderが`Failed` | recorder Job/Pod log、PVC容量、入力SRT | 他Cameraは継続する。原因とPVC上の`ready/`を保全し、新しいTakeで録画を再開する |
| Uploadが`Retrying` | uploader Pod log、S3 endpoint/network、`UploadHealthy` | timeout、5xx、rate limitは上限付き指数backoffで自動再試行する。Takeは自動停止しない |
| Uploadが`PermanentFailure` | S3 credential、bucket、既存objectの`sha256` metadata | 認証・bucket・競合を修正する。Takeが録画中なら、修正後に失敗したuploader Jobを削除するとoperatorが同じPVCから再作成する。停止後はPVCを削除せず、`ready/*.ts`を同じkeyへ手動退避する |
| Podが`ImagePullBackOff` | `kubectl describe pod`、k3d image一覧、disk容量 | `make image-build k3d-import deploy`を再実行する。開発hostの空き容量も確保する |

uploadは同じkeyとSHA-256 metadataが一致するobjectを成功済みとして扱うため再開可能である。
異なるdigestの既存objectは上書きせず恒久失敗にする。録画停止後もTakeが`Uploading`なら、
upload完了前なのでSessionやPVCを削除しない。
