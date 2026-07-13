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

### API・Custom Resource・Operator基盤phase

- `api/recording/v1alpha1`: Session Custom ResourceのGo型、scheme登録、DeepCopy生成物を配置する。
- `api/generated/clientset`: `client-gen`が生成する型付きKubernetes clientとfake clientを配置する。
- `config/crd`: `controller-gen`が生成するSession CRDとKustomize入口を配置する。
- `config/rbac`: OperatorのServiceAccount、ClusterRole、ClusterRoleBindingを配置する。
- `config/default`: CRDとRBACをまとめて検証・適用するKustomize入口とnamespaceを配置する。Operator等のworkloadは後続phaseで追加する。
- `internal/operator/session_controller.go`: Session監視、desired workload適用interface、依存障害時のstatus・Event・再queueを実装する。
- `internal/operator/validation`: Web APIと操作handlerが共有する入力validationを配置する。
- `internal/operator/httpapi`: Web UI向けHTTP server、routing、共通JSON error、Session状態取得を実装する。
- `cmd/operator/main.go`: controller-runtime manager、Session reconciler、HTTP APIの起動を行う。

### Session作成phase

- `internal/storage/session_registry.go`: S3上の`<session>/.kinugasa-session`予約objectと既存prefixによるSession名の永続予約を実装する。
- `internal/operator/session_creator.go`: 名称validation、決定的なCR名生成、現在のCRとの重複確認、S3予約、Session CR作成、idempotency処理を実装する。
- `internal/operator/httpapi/server.go`: `POST /api/v1/sessions`とSession作成errorのHTTP mappingを追加する。
- `cmd/operator/main.go`: S3 endpoint、region、bucket、path-style設定をAWS SDKへ接続し、Session作成serviceをHTTP APIへ注入する。
- `web/src/api.ts`: Web UIからOperator APIへSession作成を要求するclientを配置する。
- `web/src/App.tsx`: Session名入力、client-side validation、重複警告、作成後のSession画面への遷移を実装する。

### 映像fanout・LiveKit中継phase

- `internal/media/process.go`: child processの起動、FFmpeg progress取得、SIGINTによる正常停止、timeout時のkill、異常終了状態を実装する。
- `internal/media/component.go`: 複数processのlifecycleと`/healthz`・`/status` HTTP endpointを管理する。
- `internal/media/environment.go`: 映像component共通の環境変数読み取り・型変換を実装する。
- `internal/media/ffmpeg/fanout.go`: RIST main profileとSRT listenerをcameraごとに常駐させ、選択された入力のH.264を再encodeせずRTMP preview系とSRT録画系へ分岐するcommandを生成する。
- `internal/media/ffmpeg/ingress.go`: RTMP listenerからLiveKitのWHIP endpointへ転送するcommandを生成する。WHIP非互換入力ではpreview系だけをbaseline H.264へ変換できる。
- `cmd/video-fanout/main.go`, `cmd/livekit-ingress/main.go`: 環境変数からcommandを構築し、signalとstatus serverを含むcomponentを起動する。
- `flake.nix`: 開発・test用FFmpegを追加し、RIST、SRT、RTMP、WHIP、MPEG-TS、libx264が有効な実バイナリを固定する。

### 録画・upload component phase

- `internal/media/ffmpeg/recorder.go`: H.264を再encodeせず、20桁の連番を持つMPEG-TS segmentへ分割するffmpeg commandを生成する。
- `internal/media/recorder.go`: ffmpegがclose済みとしてsegment listへ記録したfileだけを`staging/`から`ready/`へatomic renameし、録画状態と正常終了markerをatomicに公開する。
- `cmd/video-recorder/main.go`: SRT等の入力URL、segment長、shared volume、status endpointを環境変数から受け取り、recorder lifecycleを起動する。
- `internal/storage/uploader.go`: `ready/`の逐次検出、SHA-256 metadataによるS3 objectの冪等性確認、条件付きupload、指数backoff、local state、全fileのupload完了判定を実装する。
- `cmd/video-uploader/main.go`: S3 endpoint、region、bucket、path style、SDK標準のcredential環境変数と録画識別子を受け取り、uploaderを起動する。

### Camera mutation API phase

- `internal/operator/camera_service.go`: camera名と状態の検証、使用履歴の保持、cluster全体でのNodePort予約、Session CRの競合再試行、mutationの冪等性、公開接続URL生成を実装する。
- `internal/operator/httpapi/server.go`: camera追加・削除endpointと、非同期状態・接続URL・共通error responseを公開する。
- `cmd/operator/main.go`: `PUBLIC_MEDIA_HOST`とmedia NodePort範囲をcamera serviceへ注入する。

### Camera workload・LiveKit reconcile phase

- `internal/livekit/client.go`: LiveKitの公式protocol/auth packageを用い、Ingress APIとRoom APIを最小権限のservice token付きTwirp requestとして呼び出す。
- `internal/operator/livekit_room.go`: leader上でpreview roomの存在を確認し、cluster起動時に冪等に初期化する。
- `internal/operator/livekit_ingress.go`: camera固有のWHIP ingress、participant、video trackを冪等に作成・削除し、秘密の接続URLをowner付きSecretへ保存する。
- `internal/operator/camera_workloads.go`: cameraごとの外部RIST/SRT NodePort Service、内部録画・RTMP Service、`video-fanout`・`livekit-ingress` Deploymentをreconcileし、camera statusへ集約する。削除時はWHIP bridgeのforeground削除完了後にLiveKit ingressと残りのworkloadを削除する。
- `internal/operator/session_controller.go`: camera workloadの子resourceを監視し、非同期削除中はdegradedにせず再queueする。
- `api/recording/v1alpha1/session_types.go`: camera statusに秘密情報を含まないLiveKit ingress IDを保持する。

以降のphaseでpackage・fileが確定するたびに、この節へ配置と責務を追記する。
