# Kubernetes configuration

`default/`はCRD、RBAC、Operator、Web、Redis、LiveKit server、LiveKit Ingress
service、S3接続設定を含む共通Kustomize構成である。`overlays/k3d`はlocal cluster用の
入口、`overlays/production`は実環境固有patchを重ねるための雛形とする。

checked-in Secretは開発用placeholderである。実環境ではprivate overlayまたはsecret
managerでLiveKit/S3 credentialを必ず置換し、公開host、TLS、storage class、image digest
も固定する。
