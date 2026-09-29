# Rust single binary化に向けた録画保存機能の移植調査

> **2026-09-29 更新:** 現行実装は録画開始要求以降のRIST payloadを、そのまま単一の`video.ts`へ保存する方式に変更した。録画経路では開始位置の解析やTS内部のdiscontinuity検査をしない。以下は過去の方式選定を記録した文書であり、現在の仕様は[外部インターフェース](../requirements-v2/14-specified-requirements/01-external-interfaces/external-interfaces.md)を参照。

> **2026-09-28 更新:** 実装方針はraw MPEG-TS直接保存から、`transmux`
> 0.24.1によるH.264/AACのfMP4 transmuxへ変更した。MPEG-TSは接続時から継続解析し、録画開始要求後の最初のH.264 sync sampleから保存する。以下には当初のraw TS案の調査経緯も残す。

## 概要

kinugasa-recordingをRustのsingle binaryへ再構成するにあたり、現在MediaMTXに依存している録画保存機能を移植できるか調査した。

調査の結論は次のとおりである。

- 録画保存機能はRustへ移植可能であり、single binary化の技術的な阻害要因ではない。
- 現行と同じfMP4を生成する必要がなければ、RISTで復旧済みのMPEG-TSを`.ts`として直接保存する方式を第一候補とする。
- MPEG-TS直接保存では録画fileを再構成するための完全なdemux/remuxが不要であるため、映像・音声品質と入力timestampを変化させず、MediaMTX方式よりCPU負荷と実装量を削減できる。録画開始境界を判定するための最小限のTS/PES/H.264 parserは必要になる。
- ただし、streamの任意位置からbytesを書き始めるだけでは、先頭にPAT、PMT、H.264 SPS/PPSおよびIDRが揃わない場合がある。独立して復号可能な境界を確認してから録画を開始する必要がある。
- 実機Moblinが各IDR付近でSPS/PPSを再送することを確認できない場合は、camera側の送信契約を強化するか、Rust側でMPEG-TSをdemuxしてfMP4などへremuxする。
- MediaMTXは録画以外にRTSP配信とpreview中継も担当している。録画保存の移植だけでは、MediaMTX、FFmpegおよびFFprobeへの依存はすべて解消されない。

本書の推奨は、後段パイプラインがMPEG-TSを受け入れられること、および実機streamが後述する自己完結境界の条件を満たすことを前提とした条件付きのものである。

## 調査範囲

調査日は2026年9月25日である。次を対象とした。

- kinugasa-recordingの現行録画経路とMediaMTXの責務
- MediaMTX v1.20.1のMPEG-TS入力およびfMP4 recorder実装
- MPEG-TSを録画containerとして直接保存する構成
- RustでMPEG-TSを検査またはremuxするためのcrate
- file確定、異常終了、backpressureおよび既存contractへの影響

previewの完全なRust移植、LiveKitへのWHIP送信およびAACからOpusへのtranscode実装は、本調査の主対象外とする。ただしsingle binary化に残る依存として影響を記載する。

## 現行実装

### media経路

現在の経路は次のようになっている。

```text
camera
  │ RIST / MPEG-TS
  ▼
video gateway（Rust / librist）
  │ 復旧済みpayloadにRTP headerを付加
  │ RTP / MP2T over UDP
  ▼
video worker（Go）
  │ RTP headerを除去
  │ MPEG-TS over UDP
  ▼
MediaMTX
  ├─ MPEG-TS demux
  ├─ RTSP配信
  ├─ fMP4録画
  └─ preview用stream管理
```

video gatewayが扱う`DataBlock::payload()`はすでにMPEG-TS payloadである。gatewayはこれにRTP payload type 33のheaderを付けてUDP送信している。

- [`video-gateway/src/receiver.rs`](../../video-gateway/src/receiver.rs)
- [`video-gateway/src/rtp.rs`](../../video-gateway/src/rtp.rs)

video workerはRTP headerを検証・除去し、MPEG-TS payloadをMediaMTXのUDP inputへ送信する。

- [`internal/worker/media/rtp_mpegts.go`](../../internal/worker/media/rtp_mpegts.go)

したがってgatewayとworkerを同一Rust processへ統合する場合、録画処理はRTP headerの付加と除去を経由せず、libristから受け取ったMPEG-TS payloadを直接利用できる。

### MediaMTXの録画責務

video workerはMediaMTXをchild processとして起動し、次を設定している。

- UDP MPEG-TS source
- RTSP server
- fMP4 recorder
- 1秒のrecord part
- 24時間のrecord segment
- segment create/complete hook
- HTTP APIによる録画開始・終了

該当実装は[`internal/worker/media/server.go`](../../internal/worker/media/server.go)にある。

録画commandを受け取ると、video workerはMediaMTX HTTP APIの`record`設定を切り替え、hook subprocessが作成するevent fileをpollする。録画完了時は次を実行する。

1. completed hookを待つ。
2. 1 takeにつきsegmentが1個であることを検証する。
3. `moov`、`moof`および`mdat` boxが存在することを検証する。
4. fileを同期する。
5. 最終パスへatomic renameする。
6. 親directoryを同期する。

該当実装は[`internal/worker/recording/recorder.go`](../../internal/worker/recording/recorder.go)にある。

MediaMTX v1.20.1のfMP4 recorderは、MPEG-TSをH.264 access unitやAAC access unitまでdemuxし、H.264についてはSPS/PPS、IDR、PTS/DTSおよびcomposition offsetを処理したうえで、初期化segmentと複数の`moof`/`mdat` partを生成する。

- [MediaMTX v1.20.1 `format_fmp4.go`](https://github.com/bluenviron/mediamtx/blob/v1.20.1/internal/recorder/format_fmp4.go)
- [MediaMTX v1.20.1 `format_fmp4_segment.go`](https://github.com/bluenviron/mediamtx/blob/v1.20.1/internal/recorder/format_fmp4_segment.go)
- [MediaMTX v1.20.1 MPEG-TS mapping](https://github.com/bluenviron/mediamtx/blob/v1.20.1/internal/protocols/mpegts/to_stream.go)

この全機能をRustで同じ構造のまま再実装することは可能だが、録画containerを変更できる場合には不要である。

## 推奨方式: MPEG-TS直接保存

### 基本方針

libristが復旧したMPEG-TS packetを、takeごとの`.partial.ts`へそのまま書き込む。録画終了時にfileとdirectoryを同期し、最終的な`video.ts`へrenameする。

MediaMTX自身もfMP4とMPEG-TSの両方を正式な録画形式としてサポートしている。MediaMTXのMPEG-TS録画も、188-byte packetを連結し、設定されたpart周期でdiskへflushする方式である。

- [MediaMTX: Record streams](https://mediamtx.org/docs/features/record)
- [MediaMTX: Publish with MPEG-TS](https://mediamtx.org/docs/publish/mpeg-ts)

直接保存には次の性質がある。

- H.264およびAACを再エンコードしないため、画質と音質は変化しない。
- PTS、DTSおよびPCRを変換しないため、container変換に起因する時間精度の劣化がない。
- 録画fileを再構成するための完全なdemux、H.264 DTS再構築およびMP4 box生成が不要になる。
- MPEG-TSには終了時の必須trailerがないため、processが異常終了しても最後の完全な188-byte packetまでは利用できる。
- TS overheadにより、fMP4よりfile sizeが大きくなる。
- playerによるseekやduration表示はMP4より弱く、主に後段処理用のmaster fileとして適している。

### 録画開始境界

MPEG-TS streamの途中から単純に保存を開始すると、fileの先頭に次が存在しない可能性がある。

- PAT
- PMT
- H.264 SPS
- H.264 PPS
- IDR

その場合、demuxerはtrackを認識できないか、次のparameter setとIDRまで映像を復号できない。よってrecorderには`Arming`状態を設け、録画開始commandの受信後に次の自己完結したrandom access pointまで待つ。

```text
Idle
  │ StartRecording
  ▼
Arming
  │ PAT + PMT + SPS + PPS + IDRを確認
  ▼
Recording
  │ FinishRecording
  ▼
Finalizing
  │ flush + fdatasync + rename + directory sync
  ▼
Finished
```

`StartedAt`はcommand受信時刻ではなく、実際にfileへ採用した最初のrandom access pointの時刻とする。現行MediaMTX recorderも最初のIDRを受信するまでH.264 sampleを保存しないため、最大1 GOP遅れて開始する性質は現行と同等である。

Moblinの設定import schemaには`maxKeyFrameInterval`と`bFrames`が存在する。

- [Moblin `MoblinSettingsUrl.swift`](https://github.com/eerimoq/moblin/blob/main/Moblin/Various/MoblinSettingsUrl.swift)

30 fpsで`maxKeyFrameInterval`を30に固定すれば、正常なstreamでは録画開始待ちを約1秒以内に制限できる。ただし、この設定はSPS/PPSの再送自体を保証しない。実機MoblinのMPEG-TSを取得し、各IDR付近に必要なparameter setが含まれることを別途確認する必要がある。

### streamが自己完結境界を提供しない場合

SPS/PPSがconnection開始時にしか現れないstreamをraw TSのまま途中から保存することは、安全には行えない。過去のSPS/PPSを含むTS packetを単純にfile先頭へ再掲すると、古いPTS/PCRやcontinuity counterを混入させる可能性がある。

この場合は次のいずれかを選択する。

1. camera clientの契約に、PAT/PMTの周期送信と各IDRでのSPS/PPS再送を追加する。
2. Rust側でPAT/PMT、PESおよびH.264 access unitをdemuxし、保持したcodec configurationから新しいMPEG-TSまたはfMP4をmuxする。

後者ではraw TS直接保存の単純さが失われるため、実機検証後にのみ採用判断する。

## processおよびI/O設計

### librist callbackをblockしない

librist callback内でfile writeや`fsync`を行うと、network recovery経路がstorage latencyの影響を受ける。これは現行の「RIST callbackをmedia処理でblockしない」という設計意図に反する。

推奨構成は次のとおりである。

```text
librist callback
  ├─ RIST統計・sequence観測
  └─ bounded SPSC handoff
       └─ media thread
            ├─ TS continuity検査
            ├─ PAT/PMT・H.264検査
            ├─ recording writer
            └─ preview branch
```

handoffは容量固定とし、無制限queueを使用しない。queueがoverflowした場合はpacketを黙って欠落させず、そのtakeをrecording errorへ遷移させる。camera接続やpreviewを継続できる場合でも、欠落を含む録画fileをcompletedとして公開してはならない。

この構成を採用する場合、現行要求にある「callbackとUDP出力の間にapplication独自queueを設けない」という記述は、single processの新しいmedia境界に合わせて更新する必要がある。

### file lifecycle

録画fileには次のlifecycleを適用する。

1. privateなincomplete directoryに、排他的createで`.partial.ts`を作成する。
2. 188-byte packet単位でのみwriteする。
3. write error、short write、queue overflowおよびTS continuity gapをrecording errorとして扱う。
4. `FinishRecording`後にbufferをflushし、fileを`fdatasync`または`fsync`する。
5. 最終パスが存在しないこととsymlinkでないことを再検証する。
6. 同一filesystem上で最終パスへatomic renameする。
7. 親directoryを`fsync`する。
8. 確定後にhash計算とupload queueへ渡す。

現行[`internal/worker/recording/recorder.go`](../../internal/worker/recording/recorder.go)のpath traversal対策、symlink対策、atomic renameおよびdirectory syncはRust版でも維持する。

異常終了時に残った`.partial.ts`は、現行仕様と同様にactive recordingをerroredとし、自動uploadしない。将来salvage機能を追加する場合でも、通常のcompleted fileとは別の明示的なrecovery workflowにする。

## 実験結果

### 方法

FFmpegで次の10秒streamを生成した。

- video: H.264、320x180、30 fps、GOP 30
- audio: AAC、48 kHz、128 kbps
- container: MPEG-TS
- H.264 `repeat-headers=1`
- MPEG-TS `resend_headers`

生成したTSの先頭600 packet、112,800 bytesを188-byte境界で除去し、stream途中から始まるfileを作成した。そのfileをFFmpegとFFprobeで検査した。

### 結果

- FFprobeはH.264 videoとAAC audioを認識した。
- video frame rateは`30/1`と認識された。
- FFmpegは後続のrandom access point以降を復号できた。
- file先頭では`non-existing PPS 0 referenced`が発生した。
- videoは7.6秒、210 frame、audioは約7.7秒として読み出された。

この結果から、MPEG-TSの188-byte境界だけを守れば途中切り出しが常にcleanになるわけではなく、PAT/PMT、SPS/PPSおよびIDRを考慮した開始gateが必要であることを確認した。

同じ10秒streamをFFmpegでfMP4へstream copyした場合のsizeは次のとおりだった。

| container | size |
| --- | ---: |
| MPEG-TS | 480,152 bytes |
| fMP4 | 392,152 bytes |

この合成streamではMPEG-TSが約22.4%大きかった。差はbitrate、packet充填率、PAT/PMT周期およびadaptation field量に依存するため、本番値として一般化せず、実機captureで再測定する。

## Rust crateの評価

### `mpeg2ts-reader`

[`mpeg2ts-reader`](https://docs.rs/mpeg2ts-reader/latest/mpeg2ts_reader/) 0.18.2は、MPEG-TS packet、PAT/PMT、descriptorおよびPESをpush型でparseできる。MITまたはApache-2.0 licenseである。

ローカルではRust 1.97を使用し、106 unit testsと6 doctestsがすべて成功した。Transport Stream inspectorの基盤として候補になる。

一方、H.264 access unit構築、SPS/PPS管理、IDR境界認識および録画file生成はkinugasa側で実装する必要がある。raw TS直接保存で必要な範囲に限定して利用するのが適切である。

### `transmux`

[`transmux`](https://docs.rs/transmux/latest/transmux/) 0.24.1は、streaming MPEG-TS demux、H.264/AAC、PTS/DTS、33-bit timestamp unwrap、continuity gap、CMAF/fMP4 segmenterおよびMatroska muxerを提供する。MITまたはApache-2.0 licenseである。

ローカルではRust 1.97でbuildし、420 library testsが成功した。機能面ではMediaMTX recorderの代替に最も近い。

ただし2026年に公開された比較的新しいcrateであり、releaseとAPI変更の速度が速い。録画の中核に採用する場合は次を必須とする。

- crates.ioのfloating versionではなく、検証済みrevisionを固定する。
- H.264/AACの実機fixtureをrepositoryに保持する。
- FFprobeなど独立実装によるgolden testを設ける。
- timestamp discontinuity、B-frame、audioなし、packet lossおよび長時間streamを追加試験する。
- 必要ならforkを維持できる体制を取る。

実装では再生可能性と既存contractとの互換性を優先し、この方式を採用した。0.x crateのためversionは完全固定する。

### `mp4e`

[`mp4e`](https://docs.rs/crate/mp4e/latest) 1.0.5は、dependencyを持たない小規模なpure Rust MP4/fMP4 muxerである。

ローカル試験では通常のunit testは1件だけ成功したが、doctest 13件中2件が失敗し、そのうち1件は`flush()`中の`Option::unwrap()`によるpanicだった。入力media parserも持たないため、kinugasaのproduction recorderには採用しない。

### FFmpeg library

FFmpegのMPEG-TS demuxerとMOV/MP4 muxerをRustから利用すれば、実績のあるfMP4 remuxを構築できる。FFmpegのMP4 muxerはfragmented outputに対応し、fragmented fileは通常のMP4より中断耐性がある。

- [FFmpeg Formats Documentation](https://ffmpeg.org/ffmpeg-formats.html)
- [FFmpeg License and Legal Considerations](https://ffmpeg.org/legal.html)

ただしRust bindingに加えてlibavformatなどのnative libraryが必要となる。静的linkで単一ELFにまとめる場合はbuildが複雑になり、FFmpegのlicense条件にも対応する必要がある。recordingだけのために採用する優先度は低い。

### GStreamer

GStreamerのRust製ISOBMFF pluginはCMAF/fMP4 muxerを提供し、機能と実績は十分である。

- [`gst-plugins-rs`](https://github.com/GStreamer/gst-plugins-rs)

一方、GStreamer core、registryおよびpluginの配布が必要であり、単純なsingle binaryとは相性が悪い。本計画では採用しない。

## 方式比較

| 方式 | CPU負荷 | 実装量 | 耐異常終了 | 容量 | dependency risk | 評価 |
| --- | ---: | ---: | --- | --- | --- | --- |
| raw MPEG-TS直接保存 | 最小 | 小 | 高い | 大きい | 小 | 旧提案・不採用 |
| `transmux`でfMP4 | 低い | 中 | fragment単位 | 小さい | 0.xのためversion固定 | **採用** |
| FFmpeg libraryでfMP4 | 低い | 中 | fragment単位 | 小さい | native build/license | fallback |
| GStreamer | 低い | 中 | 高い | 選択可能 | runtime/plugin | single binary方針と不一致 |
| `mp4e` | 低い | 中〜大 | 未確認 | 小さい | test不足 | 不採用 |

## contractおよび仕様への影響

> 以下はfMP4採用時点の検討記録。現行方針はraw MPEG-TSの直接保存であり、lockfileは`video.ts`・schema version `2.0`に変更済み。TS構造とdiscontinuityのオンライン検査は行わない。

現在のlockfile schemaと要求仕様は、論理パスとobject keyを`video.mp4`に固定している。

- [`contracts/lockfile/lockfile.schema.json`](../../contracts/lockfile/lockfile.schema.json)
- [外部インターフェース要求](../requirements-v2/14-specified-requirements/01-external-interfaces/external-interfaces.md)

採用方式はfMP4を生成し、論理パスとobject keyの`video.mp4`を維持する。そのため、次に記すraw MPEG-TS採用時のcontract変更は行わない。

MPEG-TSを正式形式にする場合、少なくとも次を変更する。

- 論理パスを`video.ts`へ変更する。
- content-addressed object keyのsuffixを`-video.ts`へ変更する。
- lockfile JSON Schemaのpath patternを変更する。
- 必要ならcontainerまたはmedia type fieldをlockfileへ追加する。
- 後段パイプラインがMPEG-TSを入力として受け入れることを明記する。
- 「video workerにはMediaMTXを使用する」という設計制約を廃止または置換する。
- MediaMTX fMP4 boxを前提にしたintegration testをTS向けに変更する。
- input検証をRTSPとFFprobeからRustのTS/H.264 inspectorへ移す。

既存consumerとの段階的な互換性が必要なら、lockfile schema versionを更新し、`video.mp4`と`video.ts`を同じschemaで暗黙に混在させない。

## single binary化全体に残る課題

録画保存を移植しても、現行MediaMTX経路には次が残る。

- input online/offline判定
- H.264 codecとframe rateの検証
- RTSP配信
- preview用RTSP stream
- LiveKit WHIPへのforward
- AACからOpusへのFFmpeg transcode

特に[`internal/worker/media/server.go`](../../internal/worker/media/server.go)はpreview開始時にFFmpeg subprocessを起動し、H.264をcopyしながらaudioをOpusへtranscodeしている。完全なsingle binary化には、preview branchとaudio encoderを別途Rustへ移植する必要がある。

また、現在のRust video gatewayは`rist-rs`を通じてC実装のlibristを利用する。「single binary」を単一application processの意味で使うなら問題ないが、外部shared libraryも一切持たない単一static executableを要求する場合は、libristとその暗号dependencyのstatic linkも別途検証する。

## 実装計画

### Phase 0: 実機streamの適合性確認

Moblinを使用して、audioあり・なし、B-frameあり・なしのRIST streamを取得する。最低10分間について次を解析する。

- `DataBlock::payload()`が常に188-byte packetの正の倍数か
- PATとPMTの周期
- IDR間隔
- 各IDR付近のSPS/PPS再送
- PTS、DTSおよびPCRの単調性とwrap
- continuity counter
- reconnect前後のtimestamp discontinuity

SPS/PPS再送を含む自己完結境界が安定して得られない場合は、raw TS案をそこで棄却し`transmux`案へ移る。

### Phase 1: raw TS recorder spike

production codeから独立した小さなRust spikeで次を実装する。

- librist payloadの受信
- bounded handoff
- PAT/PMTとvideo PIDの認識
- H.264 SPS/PPS/IDR検出
- `Idle`、`Arming`、`Recording`、`Finalizing`状態
- `.partial.ts`への保存
- syncとatomic rename
- FFprobe/FFmpegによる独立検証

この段階でMediaMTX版とCPU、RSS、write throughput、開始遅延およびfile sizeを比較する。

### Phase 2: worker controlとの統合

現行の`Recorder`相当interfaceをRustに実装し、consoleとのcommand/event contractを維持する。

- 重複した`StartRecording`の拒否
- take identityの検証
- input切断時のabort
- started/finished timestamp
- process restart時のactive recording error化
- finalized fileのupload queue登録
- path traversalおよびsymlink対策

MediaMTX API、hook subprocess、event directoryおよびpollingは削除する。

### Phase 3: contract移行

次を同一変更単位で更新する。

- lockfile schema
- OpenAPI exampleと後段パイプライン文書
- requirements-v2
- component dependency diagram
- object key生成
- integration test

### Phase 4: 長時間・障害試験

実機2 cameraで10分以上録画し、現行MediaMTX版と比較する。network loss試験、disk遅延、disk full、process killおよびreconnectを含める。

概算では、raw TS方式の適合性確認からproduction recorder統合まで2〜3 engineer-weeksを見込む。preview移植とconsole serverを含むRust全面移行は含まない。raw TSが成立せずfMP4 remuxが必要になった場合は、crate評価とmedia edge case対応のため追加期間を見込む。

## 受け入れ基準

raw TS方式を採用するには、少なくとも次を満たすこととする。

### 正当性

- 確定fileのsizeが188の倍数である。
- 全packetの先頭にsync byte `0x47`が存在する。
- file先頭の許容範囲内にPAT、PMT、SPS、PPSおよびIDRが存在する。
- FFprobeがH.264、29.97〜30 fpsおよびoptional audioを認識する。
- FFmpegまたは後段pipelineが先頭からerrorなく全fileをdecodeできる。
- lossなしの入力でsourceとrecordingのvideo/audio sample数が一致する。
- sourceのPTS/DTS/PCRを不必要に変更しない。

### 性能と精度

- RIST callbackの処理時間とpacket loss率が現行以下である。
- recorder有効時のCPU使用量とRSSがMediaMTX版以下である。
- 2 camera、10分録画における時間軸driftが現行以下であり、努力目標の2 frame以内を維持する。
- 正常streamの録画開始遅延が1 GOP以内である。
- disk throughputが想定最大bitrateに対して十分なmarginを持つ。

### 障害処理

- queue overflow、write error、disk fullおよびcontinuity gapを黙って無視しない。
- 上記障害時に該当RecordingCameraだけをerroredにする。
- `.partial.ts`をcompleted objectとしてuploadしない。
- `FinishRecording`成功後はfileとdirectoryが永続化されている。
- process kill後も最後の完全なTS packetまで解析できる。

## 最終判断

録画containerを変更できるという条件では、MediaMTXのfMP4 recorderをRustへ忠実に移植する必要はない。次の判断順序を推奨する。

1. 実機Moblin streamが周期的なPAT/PMTと、IDRごとのSPS/PPSを提供するか確認する。
2. 条件を満たす場合はMPEG-TS直接保存を採用する。
3. 条件を満たさない場合、後段要件を踏まえて`transmux`によるfMP4またはMatroska remuxを検証する。
4. `transmux`で必要な品質を得られない場合のみ、FFmpeg libraryの静的linkをfallbackとして検討する。

この順序なら、最も単純で高速な方式を先に検証しつつ、実機streamの性質に依存するリスクを早期に判定できる。録画保存機能については、Rust single binary化を進めてよいと判断する。
