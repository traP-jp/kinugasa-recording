# ユーザビリティ要求

## web consoleのページ構造

| パス | ページ | 役割 |
| --- | --- | --- |
| `/sessions` | Sessions | Sessionの一覧、作成および選択を行う。 |
| `/sessions/{sessionName}` | Session Console | cameraの追加・削除、映像のプレビュー、接続状態の確認、takeの開始・停止、およびuploadingなFinishedTakeの確認を行う。 |
| `/sessions/{sessionName}/takes` | Takes | FinishedTakeの一覧と状態を確認する。 |
| `/sessions/{sessionName}/takes/{takeName}` | Take Detail | FinishedTakeとcameraごとのVideoFile、upload状態、hashおよびエラーを確認する。 |

- `/`へアクセスした場合は`/sessions`へ遷移する。
- Session Consoleでは、OngoingTakeとuploadingなFinishedTakeを同時に確認できるものとする。
- cameraの接続先URLとQRコードは、CameraConnectionがactivating以外の場合、Session Console上のモーダルで確認できるものとする。
- Session Consoleでは、video gatewayがcameraクライアントからの接続を拒否した場合、対応するCameraConnectionをerrorとして、その理由とともに表示する。
