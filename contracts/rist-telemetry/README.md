# RIST telemetry contract

video gatewayは、OTLP/gRPCのMetricsServiceを使用してconsole serverへRIST統計を送信する。
すべてのinstrumentは単調増加するdelta Sumであり、隣接するdata pointの区間は重複しない。
collection intervalは5秒とする。

## Resource attributes

| attribute | 型 | 内容 |
| --- | --- | --- |
| `service.name` | string | `kinugasa-video-gateway` |
| `service.instance.id` | string | gateway PodのUID |
| `kinugasa.session.name` | string | Session名 |
| `kinugasa.camera.name` | string | camera名 |

## Metrics

すべてのdata pointは、RIST flow IDを表すint attribute `rist.flow.id`を持つ。

| instrument | unit | 内容 |
| --- | --- | --- |
| `kinugasa.rist.output.packets` | `{packet}` | libristによるARQと並べ替えの後に出力されたpacket数 |
| `kinugasa.rist.lost.packets` | `{packet}` | 復旧後のsequence列に残ったgapに対応するpacket数 |
| `kinugasa.rist.recovered.packets` | `{packet}` | ARQによって回復したpacket数 |
| `kinugasa.rist.discontinuities` | `{event}` | packet lossとして扱わなかった不連続の回数 |

同じresource、flowおよびdata point区間を持つ4つの量を1つのRIST統計として扱う。
更新がないcounterのdata pointが省略された場合、その区間の値は0とする。
RIST回復後packet loss率はこの契約に含めない。
