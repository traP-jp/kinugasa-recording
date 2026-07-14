# 開発環境の構築

この文書は、k3dを使用する開発profileの初回構築、更新、確認、破棄手順をまとめる。
映像入力や録画の操作、障害復旧は`docs/operations.md`、LANへ公開するportの一覧は
`docs/deployment.md`を参照する。

## 1. 前提

- x86_64 Linux
- Nix flakes
- 起動済みのDocker daemon
- 実機のcamera clientを使用する場合は、開発host、camera client、browserを同じLANへ接続する

開発profileのKustomize入口は`config/overlays/dev/k3d`である。このprofileはアプリに加えて、
単一nodeのGarage、`kinugasa-recording` bucket、開発専用credentialを構成する。外部のS3は
必要ない。Garageはreplicationを行わないため、実運用のobject storageとして使用しない。

## 2. 開発toolchainと依存package

repositoryのrootで開発shellへ入り、Webの依存packageを取得する。

```sh
nix develop
pnpm install --frozen-lockfile
```

format、lint、unit test、buildをまとめて確認する場合は次を実行する。

```sh
make all
```

## 3. 初回起動

k3d clusterを作成し、component imageをbuildしてclusterへimportした後、開発profileをdeployする。

```sh
make k3d-create image-build k3d-import
make deploy
./scripts/k3d-lan-check.sh
```

`make deploy`はdefault routeから開発hostのLAN IPv4 addressを検出し、camera接続URLと
LiveKitの公開URLへ反映する。複数NIC、VPN、default routeがない環境ではaddressを明示する。

```sh
PUBLIC_HOST=192.168.1.20 make deploy
PUBLIC_HOST=192.168.1.20 ./scripts/k3d-lan-check.sh
```

起動後のWeb UIは`http://<LAN IPv4>:30080`である。resourceの状態は次のcommandで確認する。

```sh
kubectl -n kinugasa-recording get deployments,statefulsets,pods,pvc
```

Garageのobjectとmetadataは`storage-garage-0` PVCへ保存される。Garage Podを再作成しても
データは維持される。

## 4. Garageの確認

GarageはLANへ公開しない。ホストからS3 APIへ接続するときは、別terminalでport-forwardする。

```sh
kubectl -n kinugasa-recording port-forward service/garage 3900:3900
```

別のterminalで、開発profileに記載されたcredentialを使ってbucketを確認する。

```sh
export S3_ENDPOINT=http://127.0.0.1:3900
export S3_BUCKET=kinugasa-recording
export AWS_ACCESS_KEY_ID=GK0123456789abcdef0123456789abcdef
export AWS_SECRET_ACCESS_KEY=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
aws --endpoint-url "$S3_ENDPOINT" s3 ls "s3://$S3_BUCKET/"
```

credentialのSSoTは`config/overlays/dev/k3d/s3-secret-patch.yaml`、接続設定のSSoTは
`config/overlays/dev/k3d/s3-config-patch.yaml`である。

## 5. 開発中の更新

componentを変更した後はimageを再build・importし、再deployする。

```sh
make image-build k3d-import deploy
```

deploy済みclusterに対するintegration testとend-to-end testは次を使用する。

```sh
make test-integration
make test-e2e
```

これらの録画testは一時的にtest専用S3 mockへ切り替え、終了時にGarageの設定へ戻す。

## 6. 停止と破棄

k3d clusterを削除する。

```sh
make k3d-destroy
```

clusterの削除によりGarageのPVCと保存objectも削除される。必要な録画objectは事前に退避する。
