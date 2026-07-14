# Kubernetes configuration

`default/`はCRD、RBAC、Operator、Web、Redis、LiveKit server、LiveKit Ingress
service、S3接続設定を含む共通Kustomize構成である。`overlays/dev/k3d`は開発profileの
local cluster用入口であり、単一nodeのGarageと開発用bucket・credentialも構成する。
`overlays/production`は実環境固有patchを重ねるための雛形とする。

checked-in Secretは開発用placeholderである。実環境ではprivate overlayまたはsecret
managerでLiveKit/S3 credentialを必ず置換し、公開host、TLS、storage class、image digest
も固定する。

k3dのGarageは開発専用であり、単一node・replicationなしである。実運用のobject storage
として使用しない。
