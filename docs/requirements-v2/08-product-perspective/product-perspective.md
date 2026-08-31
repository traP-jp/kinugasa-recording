# 製品の位置づけ

kinugasa-recording v2は、Kubernetesクラスタ上で動作する複数のサービスから構成される。

## システム構成

- console server: REST APIを通じてシステムの操作を受け付け、Kubernetes Operatorとして各サービスのライフサイクルと録画メタデータを管理する。
- web console: console serverを通じてシステムを操作し、LiveKitからcamera映像のリアルタイムプレビューを取得する。
- video gateway: RIST Main Profileの接続を待ち受け、ARQによるpacket loss回復を行い、payloadを解釈することなく復旧済みRTPを対応するvideo workerへ中継する。
- video worker: CameraConnectionごとに最大1つのPodとして配置される。そのPod内のvideo worker containerは、対応するcameraのRTP payloadを解釈し、映像形式とframe rateを検証する。要求を満たす映像をPersistentVolumeClaim上のwork volumeへ録画し、リアルタイムプレビュー用の映像をLiveKitへ中継する。録画ファイルのハッシュ計算とオブジェクトストレージへのuploadも同じvideo worker processが行い、独立したvideo uploader containerは配置しない。
- LiveKit: web consoleへリアルタイムプレビューを配信する。
