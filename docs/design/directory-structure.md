# ディレクトリ構成

今後の実装で想定する、おおまかなモノレポ構成を示す。個々のファイルやGo packageの分割までを固定するものではない。

```text
kinugasa-recording/
├── cmd/                               # Goコンポーネントのentry point
│   ├── console-server/
│   ├── video-gateway/
│   ├── video-worker/
│   └── video-uploader/
├── internal/                          # Goの内部実装
│   ├── console/                       # REST API、domain、DB、reconciliation
│   ├── gateway/                       # RIST受信とRTP中継
│   ├── worker/                        # 録画、preview、gRPC control
│   ├── uploader/                      # hash計算とobject upload
│   └── shared/                        # 複数componentで共有する基盤処理
├── web/                               # Reactによるweb console
├── contracts/                         # OpenAPI、Protocol Buffers、JSON Schema
├── deploy/                            # Kubernetes manifestと環境別設定
├── tests/                             # component間のintegration / end-to-end test
├── docs/
│   ├── design/                        # 設計図
│   └── requirements-v2/               # v2要求仕様
├── scripts/                           # code生成、build、開発支援
└── flake.nix                          # 開発・build toolchain
```

`cmd/`と`internal/`はGoの標準的な構成に寄せ、実行単位ごとに入口と内部実装を分離する。コンポーネント間で共有するデータ形式は実装packageではなく`contracts/`を正とする。生成物、ローカル依存関係および一時ファイルは上図に含めない。
