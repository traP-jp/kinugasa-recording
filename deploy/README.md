# Deployment

このディレクトリは、外部のPostgreSQL、S3互換object storage、LiveKit（Ingressを含む）を利用するKubernetes構成を提供する。

## Images

repository rootから各imageをbuildする。

```console
docker build -f deploy/images/console-server.Dockerfile -t registry.example/kinugasa/console-server:VERSION .
docker build -f deploy/images/video-gateway.Dockerfile -t registry.example/kinugasa/video-gateway:VERSION .
docker build -f deploy/images/video-worker.Dockerfile -t registry.example/kinugasa/video-worker:VERSION .
docker build -f deploy/images/web.Dockerfile -t registry.example/kinugasa/web:VERSION .
```

`video-gateway` imageはlibristの`ristreceiver`だけをruntimeとして含み、FFmpeg、FFprobeおよびGo製binaryを含まない。`video-worker` imageはMediaMTX、入力検証用のFFprobe、録画ファイルのhash計算およびobject storageへのupload機能を含む。

## Apply

1. [`secrets.example.yaml`](./secrets.example.yaml)をコピーし、実値をSecret managerなどから投入する。`VIDEO_GATEWAY_RIST_ENCRYPTION_PEPPER`には`openssl rand -base64 32`などで生成した32 byte以上のランダム値を設定する。
2. `deploy/base`のimage名と、必要に応じてPVC容量・StorageClassをoverlayで変更する。
3. CRD、RBAC、console server、web consoleを適用する。

```console
kubectl apply -f deploy/secrets.yaml
kubectl apply -k deploy
```

Web consoleのServiceはClusterIPで作成される。tailnet内のIngressまたはGatewayを`web-console:80`へ接続する。CameraごとのRIST ServiceはoperatorがLoadBalancerとして作成する。Consoleに表示するRIST URLのホスト名は`VIDEO_GATEWAY_RIST_PUBLIC_HOST`で指定する。未指定の場合はLoadBalancer Serviceが払い出したIPまたはホスト名を使用する。RIST Main ProfileのAES-256 PSKはpepper、session ID、camera identity IDからCameraごとに導出され、Camera URLの`secret`および`aes-type` query parameterとして返される。pepperを変更すると既存URLが無効になり、Cameraのworker Podも再作成されるため、収録中にはrotationしないこと。

テスト環境などで単一ホストのNodePort範囲をCameraごとのRIST接続先として使う場合は、次の3変数を設定する。operatorは範囲内の未使用NodePortをCameraごとに1つ割り当て、暗号化parameterを含む`rist://<public-host>:<node-port>?aes-type=256&secret=...`を返す。この範囲はkinugasa-recording専用とし、ホスト側でも同じUDP範囲をKubernetes nodeへ転送する。

```text
VIDEO_GATEWAY_RIST_PUBLIC_HOST=127.0.0.1
VIDEO_GATEWAY_RIST_NODE_PORT_MIN=32000
VIDEO_GATEWAY_RIST_NODE_PORT_MAX=32099
```

k3dのload balancerは複数のNginx workerでUDPをproxyするため、同一Cameraの
RISTパケットが複数のupstream socketへ分割される。100ポート全域でUDP sessionを
維持するため、クラスタ作成後にserver load balancerを1 workerへ固定する。これは
localhostへ公開したRISTポートを安定させる。

```console
./scripts/configure-k3d-udp-session-affinity.sh k3d-kinugasa-recording-v2-serverlb
```

tailnetへ公開するRISTポートはNginxを迂回し、K3s nodeのNodePortへ直接DNATする。
次の設定はhost再起動で失われるため、クラスタ作成後とhost再起動後に実行する。

```console
./scripts/configure-k3d-rist-tailnet-dnat.sh \
  100.79.104.2 k3d-kinugasa-recording-v2-server-0
```
