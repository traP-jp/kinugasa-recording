# 外部インターフェース

## cameraクライアント

- video gatewayは、kinugasa-recording側で割り当てたポートを開き、RIST Main Profileの接続を待ち受ける。
- cameraクライアントは、H.264形式かつ30 fpsの映像を送信しなければならない。音声を映像とともに送信してもよい。
- 映像形式またはframe rateが要求を満たさない接続は拒否し、CameraConnectionをerrorとして再接続を待ち受ける。
- 接続の切断を検出した場合、CameraConnectionを削除せずwaitingとして再接続を待ち受ける。対応するRecordingCameraが存在する場合は、そのRecordingCameraだけをerroredとし、OngoingTakeおよび他のRecordingCameraを継続する。
