# 製品の位置づけ

kinugasa-recording v2は、Kubernetesクラスタ上で動作する複数のサービスから構成される。

## システム構成

- console server: REST APIを通じてシステムの操作を受け付け、Kubernetes Operatorとして各サービスのライフサイクルと録画メタデータを管理する。
- web console: console serverを通じてシステムを操作し、LiveKitからcamera映像のリアルタイムプレビューを取得する。
- video gateway: cameraからRISTでH.264映像を受信し、RTPへ再パケット化してvideo hubへ中継する。
- video hub: sessionごとに配置され、複数cameraのRTP映像を集約して録画し、リアルタイムプレビュー用の映像をLiveKitへ中継する。
- video uploader: 録画ファイルのハッシュを計算し、オブジェクトストレージへアップロードする。
- LiveKit: web consoleへリアルタイムプレビューを配信する。
