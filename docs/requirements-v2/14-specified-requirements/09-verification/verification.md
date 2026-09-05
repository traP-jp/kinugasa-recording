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

## video worker container正常終了後のupload継続

- video worker Podの作成時に1つのPersistentVolumeClaimが作成され、takeの開始時には追加のPersistentVolumeClaimが作成されないことを確認する。
- 録画を終了してvideo uploader containerによるuploadを開始した後、同じPod内のvideo worker containerを正常終了させる。
- video worker containerが再起動されず、同じPod内でvideo uploader containerとshared volumeが残り、uploadが中断されずにcompletedへ遷移することを確認する。
- video worker Podとshared volumeがuploadの完了前には削除されず、volume上のすべての録画ファイルがcompletedまたはerroredになり、video uploader containerが終了した後に削除されることを確認する。

## RIST回復後packet lossの観測

- 既知のsequence numberとtimestampを持つpacket列を用い、gapがない場合、複数のgapがある場合、duplicateがある場合、遅延packetがある場合およびsequence numberがwrap-aroundする場合を検証する。
- flowの開始と再作成、cameraクライアントの再起動およびtimestampのresetがpacket lossとして数えられないことを確認する。
- RISTの往路と復路の間に制御可能なUDP loss proxyを配置し、ARQで回復可能なpacket lossと、recovery bufferの期限を超えるburst lossを別々付加する。
- ARQで回復可能なpacket lossでは、libristの回復packet数が増加し、復旧後sequence列にそのpacketのgapが残らないことを確認する。
- recovery bufferの期限を超えるburst lossでは、復旧後sequence列のgapとOpenTelemetry MetricsのRIST回復後packet loss数が一致することを確認する。
- video gatewayが出力するRTPのsequence number、timestampおよびSSRCが、RIST回復後packetのsequence number、NTP timestampおよびflow IDから要求どおりに生成されていることを確認する。
- クラスタ内で受け渡すRIST統計にRIST回復後packet loss率が含まれないことを確認する。
- 報告される各量の5秒区間が連続し、隣接区間で重複しないこと、および同じpacketまたはeventが複数の区間に計上されないことを確認する。
- web consoleが、同じ5秒区間の復旧後出力packet数とRIST回復後packet loss数からRIST回復後packet loss率を正しく算出することを確認する。
- gatewayまたはconsole serverを再起動した際に、再起動前後のCounterを差し引いた異常値が表示されないことを確認する。
- console serverを停止した状態でも、video gatewayがRISTの受信とvideo workerへのRTP送信を継続することを確認する。
- telemetry dataが5秒を超えて更新されない場合にweb consoleがstaleを表示し、分母が0の場合に0% packet lossと表示しないことを確認する。
