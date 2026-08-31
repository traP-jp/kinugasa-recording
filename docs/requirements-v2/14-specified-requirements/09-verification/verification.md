# 検証

## camera間の時間軸ドリフト

- iOSまたはiPadOSを搭載した2台の物理端末をcameraとして使用し、各端末で[Moblin](https://github.com/eerimoq/moblin)を動作させる。
- 各cameraの映像は、それぞれ独立したvideo worker containerで処理する。
- 実空間の被写体を用いた検証環境を用意し、各端末からH.264形式、30 fpsの映像を10分間送信する。
- 周期の異なる2つのメトロノームを、両方のcameraから同時に撮影できる位置に配置する。
- 各入力へ独立してネットワークジッタを付加する。ジッタ条件はTBDとし、packet lossは付加しない。
- [traP-jp/video-synchronizer](https://github.com/traP-jp/video-synchronizer)の`estimate-clock-drift`を用いて、2つのcamera間の`drift_frames_per_frame`を算出する。
- `drift_frames_per_frame`の絶対値に、実際に解析した区間のframe数を掛けて時間軸ドリフトを求める。
- 時間軸ドリフトが2 frame以下であることを確認する。信頼区間を用いた判定基準はTBDとする。
- 検証結果には、端末の機種、OS、Moblinのバージョン、およびvideo-synchronizerのcommit hashを記録する。

## frame落ち

- camera間の時間軸ドリフトと同じ物理環境および入力条件で録画する。
- 各VideoFileをデコードし、表示順のframeごとにpresentation timestampを取得する。
- presentation timestampが単調増加していることを確認し、隣接frame間の時間差からframe落ちの回数を算出する。
- frame落ちの回数をcameraごとに検証結果へ記録する。frame落ちは時間軸ドリフトとは別に評価し、合否の閾値は設けない。

## video workerによるuploadと削除

- video worker Podの作成時に1つのPersistentVolumeClaimが作成され、takeの開始時には追加のPersistentVolumeClaimが作成されないことを確認する。
- video worker containerが録画ファイルの確定、ハッシュ計算およびuploadを行い、独立したvideo uploader containerが作成されないことを確認する。
- uploadingなVideoFileを持つcameraを通常削除しようとした場合、削除が拒否され、video worker Pod、PersistentVolumeClaimおよびVideoFileの状態が維持されることを確認する。
- Session Consoleで対象cameraを削除しようとした場合、影響を受けるVideoFile、uploadのabortおよびオブジェクトストレージ上のデータ整合性を保証しないことが警告されることを確認する。
- 確認文字列が一致しない間は強制削除できず、`I_do_understand_the_danger_of_data_inconsistency_and_really_want_to_delete_this_camera`と完全一致した場合のみ強制削除できることを確認する。
- 強制削除後に対象のuploadingなVideoFileがすべてerroredになり、uploadがabortされ、video worker Pod、PersistentVolumeClaimおよびCameraConnectionが削除されることを確認する。
- 強制削除時のオブジェクトストレージ上のobjectまたはmultipart uploadの存在、完全性および後始末は検証対象としない。

## video workerの予期しない停止

- uploadingなVideoFileを処理中のvideo worker processを異常終了させる。
- 対象workerが処理していたuploadingなVideoFileがすべてerroredになり、それらのuploadが再開されないことを確認する。
- 同じworkerが録画中であった場合は対応するRecordingCameraもerroredになり、OngoingTake、他のRecordingCameraおよび他のVideoFileの状態が維持されることを確認する。
- video workerが再起動され、新しいUUIDがCameraConnectionのvideoWorkerIdに反映されることを確認する。
