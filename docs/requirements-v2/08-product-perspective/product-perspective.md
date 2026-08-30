# 製品の位置づけ

kinugasa-recording v2は、Kubernetesクラスタ上で動作する複数のサービスから構成される。

## システム構成

- console server: REST APIを通じてシステムの操作を受け付け、Kubernetes Operatorとして各サービスのライフサイクルと録画メタデータを管理する。
- web console: console serverを通じてシステムを操作し、LiveKitからcamera映像のリアルタイムプレビューを取得する。
- video gateway: RIST Main Profileの接続を待ち受け、cameraからH.264映像および音声を受信し、RTPへ再パケット化して対応するvideo workerへ中継する。
- video worker: CameraConnectionごとに最大1つのPodとして配置される。そのPod内のvideo worker containerは、対応するcameraのRTP映像をshared volumeへ録画し、リアルタイムプレビュー用の映像をLiveKitへ中継する。
- video uploader: video workerと同じPod内の独立したapplication containerとして配置され、shared volumeから録画ファイルを読み出してハッシュを計算し、オブジェクトストレージへアップロードする。video worker containerの正常終了後も、未完了のuploadを継続する。
- LiveKit: web consoleへリアルタイムプレビューを配信する。
