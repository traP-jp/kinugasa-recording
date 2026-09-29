# 外部インターフェース

## cameraクライアント

- 受信器は、cameraクライアントに払い出したポートでRIST Main Profileの接続を待ち受ける。cameraクライアントはMPEG-TSをRISTのpayloadとして送信する。
- cameraクライアントは、H.264形式かつ30 fpsの映像をMPEG-TSに含める。音声を映像とともに送信してもよい。
- 受信処理は最初のRISTデータ受信でCameraConnectionをconnectedとする。オンライン録画処理は録画開始要求以降のpayloadを順に単一の`video.ts`へ書き込み、開始位置の解析、映像形式・frame rateの検証、TS内部のdiscontinuity検査は行わない。これらの検証が必要な場合は後段パイプラインで行う。ライブプレビューでは同じpayloadをMoQのcamera broadcast内の`mpegts`トラックへ無変換で送る。サーバー側ではTSをdemuxせず、ブラウザ側でTSを再生用に変換する。
- 受信キューの溢れやファイル書き込み失敗など、実行パス上でデータを保存できなかった場合は録画を失敗させる。
- 接続の切断を検出した場合、CameraConnectionを削除せずwaitingとして再接続を待ち受ける。対応するRecordingCameraが存在する場合は、そのRecordingCameraだけをerroredとし、OngoingTakeおよび他のRecordingCameraを継続する。

## ライブプレビュー

- MoQではcamera名のbroadcastに`mpegts`トラックを1本公開する。各frameのpayloadはlibristから受け取ったMPEG-TS payloadと同一のバイト列であり、配信されたframeを順に連結するとTSバイトストリームになる。グループ境界はメディア境界を表さない。プレビュー経路で遅延によりframeが捨てられた場合は、この連続性は保証しない。
- ブラウザはグループをシーケンス順に読み、MPEG-TSのdemuxと再生用の変換を行う。途中参加時の表示にはcamera側からPAT/PMT、映像のparameter set、IDRなどが再送される必要がある。再生可能なcodecはブラウザのMedia Source Extensionsの対応範囲にも依存する。

## 後段パイプライン

後段パイプラインへ提供するlock fileは、次の参照ファイルの形式に準拠する。

- [mocap-pipeline `inputs.lock.json`](https://git.trap.jp/VirtualLive/mocap-pipeline/src/branch/second-live/experiments/recording-26-07-28/inputs.lock.json)

lock fileは、Sessionに属する録画ファイルの論理パスと、オブジェクトストレージ上のcontent-addressed objectを対応付けるJSONである。

### JSON schema

機械可読なJSON Schemaは[`contracts/lockfile/lockfile.schema.json`](../../../../contracts/lockfile/lockfile.schema.json)に定義する。

| JSON path | 型 | 値 |
| --- | --- | --- |
| `schemaVersion` | string | `"2.0"` |
| `bucket` | string | objectを格納しているオブジェクトストレージのバケット名 |
| `objects` | object | 論理パスをkey、objectの情報をvalueとするmap |
| `objects.*.key` | string | オブジェクトストレージ上のobject key |
| `objects.*.sha256` | string | 録画ファイル全体のbyte列から計算したSHA-256を、小文字16進数64文字で表した値 |
| `objects.*.size` | integer | 録画ファイルのbyte数を表す0以上の整数 |

各objectの論理パスとobject keyは次の形式とする。

```text
論理パス:  recording/{sessionName}/{takeName}/{cameraName}/video.ts
object key: recording/{sessionName}/{takeName}/{cameraName}/{sha256}-video.ts
```

`objects`のkeyには論理パスを、対応するvalueの`key`にはobject keyを設定する。例を次に示す。

```json
{
  "schemaVersion": "2.0",
  "bucket": "recording-production",
  "objects": {
    "recording/session-1/take-1/camera-1/video.ts": {
      "key": "recording/session-1/take-1/camera-1/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef-video.ts",
      "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
      "size": 1048576
    }
  }
}
```

### 収録対象

指定されたSessionに属し、stateがcompletedであるすべてのVideoFileを`objects`に含める。stateがuploadingまたはerroredであるVideoFileは含めない。
