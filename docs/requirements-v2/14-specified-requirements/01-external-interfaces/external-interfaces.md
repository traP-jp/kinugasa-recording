# 外部インターフェース

## cameraクライアント

- video gatewayは、Kubernetes Serviceによる接続先の割り当てが完了した後、そのポートを開いてRIST Main Profileの接続を待ち受ける。
- cameraクライアントは、H.264形式かつ30 fpsの映像を送信しなければならない。音声を映像とともに送信してもよい。
- 映像形式またはframe rateが要求を満たさない接続は拒否し、CameraConnectionをerrorとして再接続を待ち受ける。
- 接続の切断を検出した場合、CameraConnectionを削除せずwaitingとして再接続を待ち受ける。対応するRecordingCameraが存在する場合は、そのRecordingCameraだけをerroredとし、OngoingTakeおよび他のRecordingCameraを継続する。

## 後段パイプライン

後段パイプラインへ提供するlock fileは、次の参照ファイルの形式に準拠する。

- [mocap-pipeline `inputs.lock.json`](https://git.trap.jp/VirtualLive/mocap-pipeline/src/branch/second-live/experiments/recording-26-07-28/inputs.lock.json)

lock fileは、Sessionに属する録画ファイルの論理パスと、オブジェクトストレージ上のcontent-addressed objectを対応付けるJSONである。

### JSON schema

機械可読なJSON Schemaは[`contracts/lockfile/lockfile.schema.json`](../../../../contracts/lockfile/lockfile.schema.json)に定義する。

| JSON path | 型 | 値 |
| --- | --- | --- |
| `schemaVersion` | string | `"1.0"` |
| `bucket` | string | objectを格納しているオブジェクトストレージのバケット名 |
| `objects` | object | 論理パスをkey、objectの情報をvalueとするmap |
| `objects.*.key` | string | オブジェクトストレージ上のobject key |
| `objects.*.sha256` | string | 録画ファイル全体のbyte列から計算したSHA-256を、小文字16進数64文字で表した値 |
| `objects.*.size` | integer | 録画ファイルのbyte数を表す0以上の整数 |

各objectの論理パスとobject keyは次の形式とする。

```text
論理パス:  recording/{sessionName}/{takeName}/{cameraName}/video.mp4
object key: recording/{sessionName}/{takeName}/{cameraName}/{sha256}-video.mp4
```

`objects`のkeyには論理パスを、対応するvalueの`key`にはobject keyを設定する。例を次に示す。

```json
{
  "schemaVersion": "1.0",
  "bucket": "recording-production",
  "objects": {
    "recording/session-1/take-1/camera-1/video.mp4": {
      "key": "recording/session-1/take-1/camera-1/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef-video.mp4",
      "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
      "size": 1048576
    }
  }
}
```

### 収録対象

指定されたSessionに属し、stateがcompletedであるすべてのVideoFileを`objects`に含める。stateがuploadingまたはerroredであるVideoFileは含めない。
