# Integration and end-to-end tests

複数componentにまたがるintegration testとend-to-end testを配置する。

`make test-integration`はdeploy済みのk3d clusterに対し、Session CRからcamera
workloadとLiveKit ingressが作成・削除される一連のreconcileを検証する。先に
`make k3d-create image-build k3d-import deploy`を実行する。
