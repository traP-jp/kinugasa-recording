# 設計上の制約

kinugasa-recording v2は、CustomResourceDefinition（CRD）で定義したKubernetesカスタムリソースを使用するOperatorとして動作しなければならない。

## 実装技術

- バックエンドの実装には、原則としてGoを使用する。
- web consoleの実装には、TypeScriptおよびReactを使用する。
- video workerには、MediaMTXを使用する。
- video gatewayはRustで実装し、[`comavius/rist-rs`](https://github.com/comavius/rist-rs)を通じてlibristを使用する。
- video gatewayのRTP出力は、libristの`ristreceiver`のcallback modeと同様に、RIST回復後packetのcallback内でUDP socketへ直接送信する。video gateway独自のpacket queueをcallbackとUDP出力の間に設けない。
- video gatewayはOpenTelemetry Metrics SDKを使用し、OTLP/gRPCでconsole serverへtelemetry dataをpushする。
- 録画、preview中継、ハッシュ計算およびuploadは同じvideo worker application containerで実行し、独立したvideo uploader containerを配置しない。
- video worker Podの`restartPolicy`は`OnFailure`とする。ユーザーが要求したcamera削除による正常終了は再起動せず、予期しない停止はエラーとして扱ったうえで再起動する。
- video worker Podが使用するwork volumeにはPersistentVolumeClaimを使用し、video worker containerからmountする。
