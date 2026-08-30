# スコープ

## 対象

- cameraクライアントが接続するためのURLの払い出しと、cameraごとのvideo workerの起動
- RISTを用いて送信される映像および音声の受信
- cameraごとの映像の録画と、take単位での録画の一括開始・停止
- video worker Pod内のcontainer間でshared volumeを介した録画ファイルの受け渡し
- 録画のハッシュ計算とオブジェクトストレージへのアップロード
- 後段パイプライン向けの録画ファイルlock file JSONの提供
- camera間の、録画開始・終了時刻の数秒オーダーの誤差を許容した同期
- 録画メタデータの永続化
- システムの操作と映像のリアルタイムプレビューの閲覧が可能なweb console

## 対象外

- camera間のフレーム単位の同期
- cameraクライアントの開発
- 後段処理の実装
- application container内でのシステム内部通信に対する認証・認可の実装（Istioへ委譲する）
