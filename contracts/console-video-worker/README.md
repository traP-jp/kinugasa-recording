# console-server - video-worker gRPC規約

機械可読な契約は[`v1/console_video_worker.proto`](./v1/console_video_worker.proto)に定義する。

## 接続モデル

- `ConsoleVideoWorkerService`はconsole-serverが実装し、video-workerが起動時に`Control`双方向streamを開始する。workerから接続することで、cameraごとに生成されるPodのaddressをconsole-serverが追跡せずにcommandを送信できる。
- 1つのvideo-worker processは1つのstreamだけを使用する。streamの最初のmessageは必ず`WorkerHello`とし、`WorkerRegistered`を受信するまでeventおよびcommand resultを送信しない。
- `worker_id`はprocess起動ごとに生成するUUIDであり、再接続では同じ値を使用する。process再起動後は新しい値を使用する。console-serverは`WorkerHello`を永続化してから`WorkerRegistered`を返し、CameraConnectionの`videoWorkerId`をこの値へ更新する。
- application container内で認証・認可は実装しない。通信相手の認証・認可および暗号化は、要件どおりIstioへ委譲する。ただしconsole-serverは、hello内の`session_id`と`camera_identity_id`がreconcile対象のPod設定と一致することを検証する。
- 同じworkerまたはcameraに新しいstreamを登録した場合、console-serverは以前のstreamを`ABORTED`で終了し、以後は最新のstreamだけへcommandを送信する。

## message validation

- すべての`oneof`は、列挙された値のうち、ちょうど1つを持たなければならない。enumの`UNSPECIFIED`はwire上で使用しない。
- `worker_id`、`event_id`および`command_id`はUUIDのcanonical textual formとする。`session_id`、`camera_identity_id`および`take_id`は空でないDB識別子とする。
- `Timestamp`はProtocol Buffersのvalid range内とする。hello、event、command、command resultおよびregistrationの各時刻は省略できない。
- `InputStatus.error`はstateが`ERROR`の場合に限り必須とする。`RecordingStatus.finished_at`はterminal stateで必須、`finalized_file`は`FINISHED`の場合に限り必須、`error`は`ERROR`の場合に限り必須とする。`started_at`は最初のframeより前に失敗した`ERROR`を除き必須とする。
- `CommandResult.error`は`REJECTED`または`FAILED`の場合に限り必須とする。すべての`WorkerError`は`UNSPECIFIED`以外のcodeと空でないmessageを持つ。

最初のmessageがhelloでない場合やmessage validationに失敗した場合、console-serverはstreamを`INVALID_ARGUMENT`で終了する。Pod設定とhelloの識別子が一致しない場合は`FAILED_PRECONDITION`で終了する。

## command

console-serverはDB上のdesired stateを更新し、commandと`command_id`を永続化してから送信する。応答が失われた場合は同じ`command_id`のcommandを再送する。

- `StartRecording`: 指定takeの録画を開始する。最初のframeを録画pipelineへ渡した時刻を`started_at`とする。
- `FinishRecording`: 指定takeを停止し、MP4をflushしてcloseし、一時pathから`relative_path`へatomicにrenameする。video-uploaderから完成ファイルを参照可能にしてから`RECORDING_STATE_FINISHED`を通知する。
- `Shutdown`: 録画していないworkerを正常終了させる。workerはterminalな`CommandResult`を送信してstreamを閉じ、exit status 0で終了する。録画中は`REJECTED`を返す。Podとshared volumeの削除可否はuploaderの状態を含めてconsole-serverが判断する。

workerは適用済みの`command_id`とterminal resultをshared volumeへ永続化する。同じcommandを再受信した場合は処理を繰り返さず、保存済みのresultを返す。同じtakeに対して異なる`command_id`で同じdesired stateが要求された場合も、安全に収束済みなら`ALREADY_APPLIED`を返す。

workerはcommandをstream上の受信順に1つずつ適用する。`APPLIED`または`FAILED`を返す前に、対応する録画状態とeventをshared volumeへ永続化する。

`relative_path`はshared-volume mountからの相対pathである。workerは絶対path、空path、path componentが`.`または`..`であるpath、およびmount外へ解決されるsymbolic linkを拒否する。v1で完成ファイルのmedia typeは`video/mp4`とする。

## eventと状態同期

- workerはcamera入力の`WAITING`、`CONNECTED`、`ERROR`への変化を`input_status_changed`で通知する。切断時は`WAITING`を通知し、録画中ならさらに`RECORDING_STATE_ERROR`を通知する。入力形式またはframe rateを拒否した場合は対応するerror codeを付けた`ERROR`を通知し、その後も再接続を待ち受ける。
- workerは録画状態の変化を`recording_status_changed`で通知する。`FINISHED`はファイルのflush、close、renameおよびuploaderへの公開が完了したことを表す。hash、object keyおよびupload結果はvideo-uploaderの責務なので、この契約には含めない。
- `StartRecording`または`FinishRecording`の実行失敗で録画が継続不能になった場合、workerは`FAILED`のcommand resultに加えて、同じerrorを持つ`RECORDING_STATE_ERROR` eventを保存して送信する。単に現在状態と両立しないcommandを受けた場合は`REJECTED`だけを返し、録画状態を変更しない。
- workerはeventを送信前にshared volume上のoutboxへ保存し、console-serverからevent IDのacknowledgementを受け取るまで再接続のたびに再送する。console-serverはeventによるDB更新とevent IDの重複排除記録を同一transactionでcommitした後にだけacknowledgeする。
- `WorkerHello.snapshot`はhello送信時点の入力状態と、存在する場合は最新の録画状態を含む。`last_event_sequence`までのeventの効果はsnapshotに反映済みである。console-serverはsnapshotをcommitした後、同じworker IDから再送されたsequence以下のeventを状態へ再適用せず、重複排除記録だけを残してacknowledgeする。terminalな録画状態は対応eventがacknowledgeされるまでsnapshotに保持する。
- eventの`sequence`はworker IDごとに1から始まる欠番のない単調増加値とし、再接続してもresetしない。process再起動でworker IDが変わった場合は、新しいsnapshotが旧processの最終状態を引き継ぎ、sequenceを1から開始する。旧processのeventは新しいstreamから再送しない。
- eventおよびcommandはat-least-onceで配送される。stream内の順序は保持されるが、再接続をまたいだ重複を許容する。UUID形式の`event_id`と`command_id`を冪等性keyとして扱う。

## 障害時の規則

- gRPC streamの切断、deadline超過またはconsole-serverの停止だけを理由に、入力受付、preview、録画またはファイル確定処理を停止してはならない。workerは指数backoff付きで再接続する。
- command実行中にstreamが切断された場合も処理を完了し、eventとresultを永続化する。再接続後にsnapshotとoutboxを送信する。
- process起動時にshared volume上の状態が旧worker IDによる`STARTING`、`RECORDING`または`FINALIZING`を示す場合、その録画を再開したり完成ファイルとして公開したりしない。新しいworkerは一時ファイルを隔離し、`ERROR_CODE_RECORDING_INTERRUPTED`のterminal stateをsnapshotに含める。
- video-worker processの予期しない終了はKubernetesのPod/container statusからconsole-serverが検出する。gRPC切断だけではprocess終了と判定しない。
- `google.protobuf.Timestamp`はevent発生元のwall clockをUTCとして表す。時刻は実際のmedia処理eventを表し、commandの送受信時刻で代用しない。

## 状態の対応

| gRPCで観測した状態 | console-serverのdomain state |
| --- | --- |
| `INPUT_STATE_WAITING` | CameraConnection `waiting` |
| `INPUT_STATE_CONNECTED` | CameraConnection `connected` |
| `INPUT_STATE_ERROR` | CameraConnection `error`と`WorkerError` |
| `RECORDING_STATE_RECORDING` | RecordingCamera `recording`と`startedAt` |
| `RECORDING_STATE_ERROR` | 対応するRecordingCameraだけを`errored`にする |
| `RECORDING_STATE_FINISHED` | `finishedAt`と完成ファイルを用いてVideoFileのupload開始へ進む |

`STARTING`と`FINALIZING`はworker内部の過渡状態であり、domain stateを追加しない。あるcameraの`RECORDING_STATE_ERROR`はOngoingTakeや他のRecordingCameraを変更しない。
