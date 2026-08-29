# 設計上の制約

kinugasa-recording v2は、Kubernetes Operatorとして動作しなければならない。

## 実装技術

- バックエンドの実装には、原則としてGoを使用する。
- web consoleの実装には、TypeScriptおよびReactを使用する。
- video hubには、MediaMTXを使用する。
- video gatewayには、libristの`ristreceiver`を使用する。
