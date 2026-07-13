# kinugasa-recording コードベース構成

## 1. 方針

Goで実装するKubernetes Operatorと映像処理コンポーネント、Reactで実装するWeb UI、Kubernetes関連の設定を1つのリポジトリで管理する。

- Goコードは単一のGo moduleとする。
- 実行可能なコンポーネントと、その内部実装を分離する。
- Web UIはpnpmとViteを用いた独立したworkspaceとする。
- 各コンポーネントを個別のDocker imageとしてビルド可能にする。
- Kubernetes Operatorの一般的な構成に合わせ、CRDやRBAC等をまとめて管理する。
- このドキュメントでは安定した責務の境界のみを定め、詳細なpackageやfileの配置は実装時に決定する。

## 2. ディレクトリ構成

```text
kinugasa-recording/
├── api/                  # Custom ResourceとHTTP APIの定義
├── cmd/                  # 各Goコンポーネントのentry point
│   ├── operator/
│   ├── video-fanout/
│   ├── video-recorder/
│   ├── video-uploader/
│   └── livekit-ingress/
├── internal/             # Goコンポーネントの内部実装
│   ├── operator/
│   ├── media/
│   └── storage/
├── web/                  # React + ViteによるWeb UI
├── config/               # CRD、RBAC、Kustomize、LiveKit等の設定
├── build/                # Docker imageのbuild定義
├── scripts/              # k3dの操作やcode generation等のscript
├── test/                 # 複数コンポーネントにまたがるtest
├── docs/                 # 要件や設計のdocument
│   ├── requirements.md   # 人間が管理する要件のSSoT
│   ├── design.md         # CR、HTTP API、映像経路、録画・uploadの初期設計
│   ├── codebase.md       # package・file配置と責務
│   └── todo.md           # 未完了タスク
├── Makefile              # build、test、deploy等の共通entry point
├── flake.nix             # 開発toolchainを固定するNix flake
├── flake.lock
├── go.mod
├── package.json
├── pnpm-lock.yaml
└── pnpm-workspace.yaml
```

## 3. 各ディレクトリの責務

### `api/`

Kubernetes Custom Resourceの型と、OperatorがWeb UIへ公開するHTTP APIの契約を配置する。session、camera、takeの操作はこのAPIを通して行う。CRDやAPI clientの自動生成物も、生成元の近くで管理する。

### `cmd/`

Docker containerとして実行するGoプログラムのentry pointを配置する。ここには起動処理のみを置き、主要な実装は`internal/`へ分離する。

- `operator`: Custom Resourceの作成・監視、workloadの制御、Web UI向けAPIの提供
- `video-fanout`: RIST/SRTで受け取った映像の録画系・preview系への分岐
- `video-recorder`: 映像のMPEG-TS形式での録画
- `video-uploader`: 録画ファイルのS3互換object storageへの逐次upload
- `livekit-ingress`: preview映像のLiveKitへの中継

### `internal/`

Goで実装する内部packageを責務ごとに配置する。

- `operator`: session、camera、takeのreconcile処理、Web API、Kubernetes workloadの組み立て
- `media`: ffmpegの制御、映像の分岐・録画・LiveKit連携
- `storage`: S3互換object storageへのupload、retry、session名の重複確認

複数領域から利用する処理は、実際に共有が必要になった時点で`internal/`直下へ追加する。

### `web/`

React、TypeScript、Viteを用いたWeb UIを配置する。sessionの作成、cameraの管理、takeの操作、LiveKitによるpreview、接続先QRコードの表示を担当する。

### `config/`

CRD、RBAC、Operator、Web UI、LiveKit等のKubernetes manifestを管理する。共通のbaseとk3d向け・実運用向けの差分はKustomizeで分離する。

### `build/`

各コンポーネントを個別のDocker imageとしてbuildするための定義を配置する。

### `scripts/`

k3dによる開発・テスト環境の構築、Custom ResourceやAPI clientの生成等、開発・運用で使用するscriptを配置する。sessionの作成はscriptの責務とせず、Web UIとOperatorを通して行う。

### `test/`

k3d、LiveKit、S3互換object storage等を使用するintegration testとend-to-end testを配置する。end-to-end testではsessionの作成からcamera、takeの操作までを検証する。GoとReactのunit testは対象コードの近くに配置する。

## 4. 依存関係の方針

```text
cmd ──> internal ──> api
web ──> HTTP API
config ──> buildした各Docker image
```

- `cmd/`間で直接依存しない。
- 映像処理自体はffmpegとLiveKitに委譲し、Goコードはprocessの制御と状態管理を担当する。
- Web UIからKubernetes APIを直接操作せず、OperatorのHTTP APIを経由する。
- OperatorはsessionごとのCustom Resourceを管理し、そのdesired stateを基にcameraやtakeに必要なworkloadを制御する。

## 5. Custom Resourceの方針

初期実装では、sessionごとに`Session` Custom Resourceを作成する。cameraとtakeの定義および状態を対応するSessionに関連付け、名前の一意性と使用履歴を管理する。

Sessionの作成時には、同じ名前がCustom Resourceに存在しないことに加え、S3互換object storage上で現在または過去に使用されていないことを確認する。session名はbucket内、camera名とtake名はsession内で一意とする。

CameraやTakeを独立したCustom Resourceへ分割するかどうかは、リソースサイズやlifecycle、RBAC等の要件が具体化した時点で判断する。

## 6. データの方針

録画ファイルは、要件に従ってS3互換object storage上で次の階層になるようにobject keyを構成する。

```text
session名/
└── take名/
    └── camera名/
        └── video file(s)
```

認証情報はKubernetes Secret、endpointやbucket等の設定はConfigMapまたは環境変数を通して各コンポーネントへ渡す。

## 7. 確定したfile配置

### 設計phase

- `docs/design.md`: Session Custom Resourceのschemaと状態遷移、名称予約、Web UI向けHTTP API、RIST/SRTからLiveKit・録画への映像経路、録画fileとuploader間の契約、障害statusを管理する。
- `docs/requirements.md`: 実装判断で変更せず、引き続き要件のSSoTとする。
- `docs/todo.md`: 未完了項目だけを保持する。

### Repository基盤phase

- `flake.nix`, `flake.lock`: Go、Node.js、pnpm、make、lintおよびcode generation toolのversionを固定する。
- `Makefile`: Go/Webのformat、lint、test、buildと、code generation、image build、deployの共通entry pointを提供する。
- `.golangci.yml`: Go lintとformatの共通設定を保持する。
- `cmd/<component>/main.go`: `operator`、`video-fanout`、`video-recorder`、`video-uploader`、`livekit-ingress`のentry pointとする。
- `api/doc.go`: 公開するKubernetes APIとHTTP API定義のpackage起点とする。
- `internal/{operator,media,storage}`: 各責務の内部package起点とする。
- `package.json`, `pnpm-workspace.yaml`, `pnpm-lock.yaml`: pnpm workspaceとroot commandを管理する。
- `web/`: React、TypeScript、Vite、Vitest、ESLint、PrettierによるWeb UI workspaceとする。
- `scripts/generate.sh`: controller-genとclient-genによるCRD、DeepCopy、Kubernetes API clientの生成入口とする。
- `config/`, `build/`, `test/`: Kubernetes設定、container build、結合testを各READMEとともに開始する。

以降のphaseでpackage・fileが確定するたびに、この節へ配置と責務を追記する。
