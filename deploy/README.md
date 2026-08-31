# Deployment

このディレクトリは、外部のPostgreSQL、S3互換object storage、LiveKit（Ingressを含む）を利用するKubernetes構成を提供する。

## Images

repository rootから各imageをbuildする。

```console
docker build -f deploy/images/console-server.Dockerfile -t registry.example/kinugasa/console-server:VERSION .
docker build -f deploy/images/video-gateway.Dockerfile -t registry.example/kinugasa/video-gateway:VERSION .
docker build -f deploy/images/video-worker.Dockerfile -t registry.example/kinugasa/video-worker:VERSION .
docker build -f deploy/images/video-uploader.Dockerfile -t registry.example/kinugasa/video-uploader:VERSION .
docker build -f deploy/images/web.Dockerfile -t registry.example/kinugasa/web:VERSION .
```

`video-gateway` imageはlibristの`ristreceiver`、`video-worker` imageはWHIP対応FFmpegとMediaMTXをbuild時に検査する。

## Apply

1. [`secrets.example.yaml`](./secrets.example.yaml)をコピーし、実値をSecret managerなどから投入する。
2. `deploy/base`のimage名と、必要に応じてPVC容量・StorageClassをoverlayで変更する。
3. CRD、RBAC、console server、web consoleを適用する。

```console
kubectl apply -f deploy/secrets.yaml
kubectl apply -k deploy
```

Web consoleのServiceはClusterIPで作成される。tailnet内のIngressまたはGatewayを`web-console:80`へ接続する。CameraごとのRIST ServiceはoperatorがLoadBalancerとして作成する。

テスト環境などで単一ホストのNodePort範囲をCameraごとのRIST接続先として使う場合は、次の3変数を設定する。operatorは範囲内の未使用NodePortをCameraごとに1つ割り当て、`rist://<public-host>:<node-port>`を返す。この範囲はkinugasa-recording専用とし、ホスト側でも同じUDP範囲をKubernetes nodeへ転送する。

```text
VIDEO_GATEWAY_RIST_PUBLIC_HOST=127.0.0.1
VIDEO_GATEWAY_RIST_NODE_PORT_MIN=32000
VIDEO_GATEWAY_RIST_NODE_PORT_MAX=32099
```
