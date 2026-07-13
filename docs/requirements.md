# kinugasa-recording 要件

## 1. 背景・目的

### 1.1 背景
traP VirtualLive Projectの現行のモーションキャプチャパイプラインは、NDIとTouch Designerを用いた複数のスマートフォンからの映像入力の一括録画を行っている。現状の録画システムは、以下の課題を抱えている。
- パフォーマンスの低さによって、ホストPCとして利用できる機材が限られている。
- 運用にTouch Designerの知識を必要としている。
- 後段の姿勢推定処理を行う環境までの動画ファイルの受け渡しが手作業で行われている。
- 不自由なプロトコルに依存している。

### 1.2 目的
`kinugasa-recording`は、NDIとTouch Designerを用いた現行のモーションキャプチャパイプラインの課題を解決することを目的とする。

### 1.3 成功条件
- [KPI-001] Let's Note CF-SRで動作する。
- [KPI-002] Webブラウザから操作できる。
- [KPI-003] 動画ファイルを逐次的にオブジェクトストレージにアップロードできる。
- [KPI-004] RISTやSRTといった標準的かつ自動再送要求に対応したプロトコルを用いる。
- [KPI-005] 既存システムと同等以上の機能を提供する。
    - [KPI-005-1] 複数スマートフォンからの映像入力の一括録画(takeの録画)。録画開始時刻の同期は不要である。
    - [KPI-005-2] 映像のリアルタイムプレビュー
    - [KPI-005-3] ユーザーによるsession、take、cameraの命名。これはsessionは同一バケット内で一意、takeとcameraはsession内で一意であることを保証する。すべての名前は英数字とハイフンのみで構成される。
    - [KPI-005-4] cameraの追加・削除
- [KPI-006] 映像入力の接続先URLをQRコードとして表示できる。

## 2. スコープ

### 2.1 対象
映像入力の受取、録画、オブジェクトストレージへのアップロードを対象とする。

### 2.2 対象外
cameraクライアントの開発、オブジェクトストレージの管理、camera間の精密な同期、姿勢推定処理は対象外とする。

### 2.3 前提・制約
- 映像入力はRIST main profileあるいはSRTプロトコルで送信される。両者に対応する必要がある。
- 映像入力はH.264形式で送信される。
- 映像関連の処理は`ffmpeg`バイナリとlivekitを用いて行う。
- 各スマートフォン及びホストPCは同一のLAN内に存在する。
- すべてのコンポーネントをdockerコンテナとして提供する。
- kubernetesを用いる。テスト及び実運用はk3dを用いて行う。
- オブジェクトストレージはS3互換である。
- すべての録画ファイルはMPEG-TSコンテナに格納される。
- サーバープロセスはkubernetesのoperatorとして動作し、クラスタ内のメタデータ永続化にはcustom resourceを用いる。

## 3. ステークホルダー
開発と運用を同一チームが担当するため省略。

## 4. システムコンテキスト

システムの境界、外部システム、利用者を図示する。

```mermaid
flowchart LR
    object_storage[S3 compatible object storage]
    subgraph kinugasa_recording[kinugasa-recording]
        direction TB
        livekit[LiveKit]
        subgraph camera_related_components[camera related components]
            direction TB
            video_fanout_proxy[video fanout proxy]
            video_recorder[video recorder]
            video_uploader[video uploader]
            livekit_ingress[livekit ingress]
        end
        k8s_operator[kubernetes operator]
    end

    subgraph smartphone[smartphone]
        direction TB
        camera_client[camera client]
    end

    mocap_team[mocap team]

    mocap_team -->|WebUI| k8s_operator
    camera_client -->|RIST/SRT| video_fanout_proxy
    video_fanout_proxy -->|RIST/SRT| video_recorder
    video_recorder -->|shared volume| video_uploader
    video_uploader -->|upload| object_storage
    video_fanout_proxy -->|RTMP| livekit_ingress
    livekit_ingress -->|WHIP| livekit
    livekit -->|broadcast| mocap_team
    k8s_operator -->|create/update/delete| camera_related_components
```

## 5. ユースケース
UC-001及びUC-004は、UC-002の実行中には行われない。

### UC-001: cameraの追加
- アクター: mocapチーム・スマートフォンの所有者
- 事前条件: スマートフォンとホストPCが同一のLAN内に存在する。
- トリガー: camera追加ボタンを押す。
#### 基本フロー
1. mocapチームがcameraの名前を入力し、camera追加ボタンを押す。
2. スマートフォンの所有者がWebUI上のQRコードを読み取る。
3. cameraクライアントが起動し、ホストPCに接続する。

#### 例外フロー
- 1a. 指定されたcameraの名前が既に存在する、あるいは過去に使用されたことがある場合。
    1. 追加を行わず、WebUI上に警告を表示する。

#### 完了条件
- cameraクライアントがホストPCに接続される。
- mocapチームがWebUI上でcamera映像のプレビューを確認できる。

### UC-002: takeの録画
- アクター: mocapチーム
- 事前条件: 使用したいcameraについて、UC-001が完了している。mocapチームがUC-003によりcamera映像が利用できることを確認している。
- トリガー: take開始ボタンを押す。

#### 基本フロー
1. mocapチームがtakeの名前を入力し、使用するcameraを選択し(未指定の場合は全選択)、take開始ボタンを押す。
2. ホストPCが使用可能なcameraに対して録画開始の指示を送信する。
3. camera関連のコンポーネントが録画と逐次アップロードを開始する。
4. mocapチームがtake停止ボタンを押す。
5. オブジェクトストレージへのアップロードが完了する。

#### 例外フロー
- 1a. 指定されたtakeの名前が既に存在する、あるいは過去に使用されたことがある場合。
    1. 録画を行わず、WebUI上に警告を表示する。
- 3a. 録画中にcameraの切断が検知された場合
    1. WebUI上に警告を表示する。自動停止は行わない。
- 3b. 録画中にオブジェクトストレージへのアップロードが失敗した場合
    1. WebUI上に警告を表示する。自動停止は行わない。
- 5a. オブジェクトストレージへのアップロードが完了しなかった場合
    1. WebUI上に警告を表示する。

#### 完了条件
- アップロードが終了する。

### UC-003: camera映像のプレビュー
- アクター: mocapチーム
- 事前条件: スマートフォンとホストPCが同一のLAN内に存在する。
- トリガー: 確認したいcameraについて、UC-001が完了した。

#### 基本フロー
1. mocapチームがWebUI上でcamera映像のプレビューを確認する

#### 例外フロー
1a. cameraクライアントが切断されている場合
    1. WebUI上に警告を表示する。

#### 完了条件
- 当該のcameraについて、UC-004が開始される。

### UC-004: cameraの削除
- アクター: mocapチーム
- 事前条件: 使用したいcameraについて、UC-001が完了している。
- トリガー: camera削除ボタンを押す

#### 基本フロー
1. mocapチームがcamera削除ボタンを押す
2. ホストPCが対応するコンテナを停止する

#### 例外フロー
なし

#### 完了条件
- すべての対応するコンテナが停止する。

### UC-005: オブジェクトストレージからの動画ファイルの取得
- アクター: mocapチーム
- 事前条件: UC-002が完了している。
- トリガー: `kinugasa-recording`の対象外のフローであるため省略。

#### 基本フロー
1. session名、take名、camera名を指定してオブジェクトストレージから動画ファイルを取得する。

#### 例外フロー
なし

#### 完了条件
なし

### UC-006: セッションの作成
- アクター: mocapチーム
- 事前条件: なし
- トリガー: session追加ボタンを押す。

#### 基本フロー
1. mocapチームがsessionの名前を入力し、session追加ボタンを押す。

#### 例外フロー
1a. オブジェクトストレージ上あるいはcustom resourceに指定されたsessionの名前が既に存在する、あるいは過去に使用されたことがある場合。
    1. 追加を行わず、WebUI上に警告を表示する。

#### 完了条件
- クラスタが起動する。

## 6. 機能要件

### FR-001: cameraの追加
- UC-001を実現する。
#### FR-001-1: custom resourceの更新
cameraの追加時に、custom resourceを更新する。
#### FR-001-2: コンテナの起動
対応する`video fanout proxy`、`livekit ingress`を起動する。
#### FR-001-3: LiveKitの更新
`livekit ingress`の起動後、LiveKitのルームにcamera入力を追加する。
#### FR-001-4: QRコードの生成
WebUI上で、起動されたコンテナに接続するためのURLをQRコードとして生成する。

### FR-002: takeの録画
- UC-002を実現する。
#### FR-002-1: custom resourceの更新
takeの録画開始/停止時に、custom resourceを更新する。
#### FR-002-2: コンテナの起動
対応する`video recorder`、`video uploader`を起動する。
#### FR-002-3: アップロード
`video uploader`は、録画中に生成されたMPEG-TSコンテナを逐次的にオブジェクトストレージにアップロードする。
#### FR-002-4: 録画
`video recorder`は、録画中に生成されたMPEG-TSコンテナをshared volumeに保存する。

### FR-003: camera映像のプレビュー
- UC-003を実現する。

#### FR-003-1: LiveKitの初期化
クラスタ起動時にLiveKit上にルームを1つ作成する。

#### FR-003-2: ユーザーによるルームへの参加
WebUIからのリクエストに応じてルームへの参加を許可する。

### FR-004: cameraの削除
- UC-004を実現する。
#### FR-004-1: custom resourceの更新
cameraの削除時に、custom resourceを更新する。
#### FR-004-2: コンテナの停止
対応する`video fanout proxy`、`livekit ingress`を停止する。
#### FR-004-3: LiveKitの更新
`livekit ingress`の停止前に、LiveKitのルームからcamera入力を削除する。

### FR-005: オブジェクトストレージにおける擬似的なディレクトリ構造の提供
- UC-005を実現する。
#### FR-005-1: ディレクトリ構造の詳細
```
- session名/
    - take名/
        - camera名/
            - video file(s)
```

### FR-006: セッションの作成
- UC-006を実現する。
#### FR-006-1: custom resourceの作成
sessionの作成時に、custom resourceを作成する。

## 7. 非機能要件
実運用環境が開発環境に近いため省略。

## 用語集
- session: 同じ部屋で連続した時間帯に録画するtakeの集合。
- take: 複数のcameraを用いて同時に録画する単位。
- camera: 1台のスマートフォンからの映像入力。
- video file: 1つのcameraからの映像入力を1つのtakeにおいて録画した結果として生成される動画ファイル。オブジェクトストレージにアップロードされる最小単位。
