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
