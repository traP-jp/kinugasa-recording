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
- Session Consoleでは、video workerが映像形式またはframe rateの不適合を検出した場合、対応するCameraConnectionをerrorとして、その理由とともに表示する。
- cameraの削除によって削除されるvideo workerがuploadingなVideoFileを処理している場合、Session Consoleは、uploadがabortされてVideoFileがerroredになること、およびオブジェクトストレージ上のデータの整合性を保証しないことを警告する。
- 前項の警告では、影響を受けるVideoFileを表示し、ユーザーが`I_do_understand_the_danger_of_data_inconsistency_and_really_want_to_delete_camera`と入力した場合に限り強制削除を実行できる。文字列の比較は大文字と小文字を区別した完全一致とする。
