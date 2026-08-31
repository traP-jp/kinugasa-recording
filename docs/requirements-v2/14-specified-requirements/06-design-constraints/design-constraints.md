# 設計上の制約

kinugasa-recording v2は、CustomResourceDefinition（CRD）で定義したKubernetesカスタムリソースを使用するOperatorとして動作しなければならない。

## 実装技術

- バックエンドの実装には、原則としてGoを使用する。
- web consoleの実装には、TypeScriptおよびReactを使用する。
- video workerには、MediaMTXを使用する。
- video gatewayには、libristの`ristreceiver`を使用する。
- 録画、preview中継、ハッシュ計算およびuploadは同じvideo worker application containerで実行し、独立したvideo uploader containerを配置しない。
- video worker Podの`restartPolicy`は`OnFailure`とする。ユーザーが要求したcamera削除による正常終了は再起動せず、予期しない停止はエラーとして扱ったうえで再起動する。
- video worker Podが使用するwork volumeにはPersistentVolumeClaimを使用し、video worker containerからmountする。
