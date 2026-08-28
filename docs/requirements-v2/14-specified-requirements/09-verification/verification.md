# 検証

## camera間の時間軸ドリフト

- iOSまたはiPadOSを搭載した2台の物理端末をcameraとして使用し、各端末で[Moblin](https://github.com/eerimoq/moblin)を動作させる。
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
