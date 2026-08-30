# console-server - video-uploader gRPC規約

video-uploaderはshared volume上のupload manifestがterminalになった後、`ReportUpload`を呼び出す。
console-serverがVideoFileとFinishedTakeへの効果を同一transactionでcommitしてからacknowledgementを返すまで、同じreportを指数backoff付きで再送する。

- `(take_id, camera_identity_id)`を冪等性keyとし、同じterminal reportの再送を許容する。異なるterminal reportへの変更は`FAILED_PRECONDITION`とする。
- `COMPLETED`では`object_key`、32 byteのSHA-256、0以上の`size`が必須で、`error`は空とする。
- `ERRORED`では空でない`error`が必須。object metadataは使用しない。
- `relative_path`、録画開始・終了時刻はworkerが公開したfinalized recordingと完全一致しなければならない。
- application container内で認証・暗号化は行わずIstioへ委譲する。console-serverはreportのsession、camera、takeの関係をDBで検証する。
- uploaderはconsole-serverの停止だけを理由にuploadを中断しない。object uploadとterminal manifest保存を完了した後、reportだけを再送する。
