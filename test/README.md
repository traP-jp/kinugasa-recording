# Integration and end-to-end tests

複数componentにまたがるintegration testとend-to-end testを配置する。

`make test-integration`はdeploy済みのk3d clusterに対し、Session CRからcamera
workloadとLiveKit ingressが作成・削除される一連のreconcileを検証する。先に
`make k3d-create image-build k3d-import deploy`を実行する。

`test/integration/media-fanout.sh`はRIST main profileとSRTでH.264 test streamを
cluster内のcamera入力Serviceへ送り、LiveKit接続状態、`connectedProtocol`と
`lastFrameAt`、内部recording SRT branchを検証する。`MEDIA_SENDER_HOST`にLAN IPv4を
指定すると、hostの公開UDP NodePort経由で同じtestを実行する。

`test/integration/recording-upload.sh`はtest専用S3 mockをk3d node内で起動し、
録画中のMPEG-TS逐次upload、object key階層、停止後のupload完了とcleanupに加え、
S3の一時障害からのretry復旧と恒久障害のstatus反映を検証する。

`make test-e2e`は公開Web HTTP APIからSession作成、2台camera追加とLAN SRT入力、
preview接続、未指定時の全camera take、明示的な1台選択、camera別の逐次upload、停止、
camera削除を順に実行する。Session・Camera・Takeの使用済み名が拒否されることも確認する。
