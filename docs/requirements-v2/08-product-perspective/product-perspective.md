# 製品の位置づけ

kinugasa-recording v2は、Kubernetesクラスタ上で動作する複数のサービスから構成される。

## システム構成

- console server: REST APIを通じてシステムの操作を受け付け、Kubernetes Operatorとして各サービスのライフサイクルと録画メタデータを管理する。
- web console: console serverを通じてシステムを操作し、LiveKitからcamera映像のリアルタイムプレビューを取得する。
- video gateway: RIST Main Profileの接続を待ち受け、cameraからH.264映像および音声を受信し、RTPへ再パケット化して対応するvideo workerへ中継する。
- video worker: CameraConnectionごとに最大1つのcontainerとして配置され、対応するcameraのRTP映像を録画し、リアルタイムプレビュー用の映像をLiveKitへ中継する。
- video uploader: 録画ファイルのハッシュを計算し、オブジェクトストレージへアップロードする。
- LiveKit: web consoleへリアルタイムプレビューを配信する。
