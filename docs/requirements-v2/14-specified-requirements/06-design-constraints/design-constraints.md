# 設計上の制約

kinugasa-recording v2は、CustomResourceDefinition（CRD）で定義したKubernetesカスタムリソースを使用するOperatorとして動作しなければならない。

## 実装技術

- バックエンドの実装には、原則としてGoを使用する。
- web consoleの実装には、TypeScriptおよびReactを使用する。
- video workerには、MediaMTXを使用する。
- video gatewayには、libristの`ristreceiver`を使用する。
- video workerとvideo uploaderは同じPod内の別々のapplication containerとして配置し、Kubernetesのnative sidecar containerとしては扱わない。
- video worker Podの`restartPolicy`は`OnFailure`とし、正常終了したvideo worker containerを再起動せず、video uploader containerの終了までPodを存続させる。
- video worker Podが使用するshared volumeにはPersistentVolumeClaimを使用し、Pod内の両containerからmountする。
