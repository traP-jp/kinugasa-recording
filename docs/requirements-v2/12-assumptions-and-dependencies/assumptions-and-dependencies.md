# 前提条件および依存関係

- RIST Main Profileのprotocol処理、ARQ、並べ替えおよび統計情報の生成はlibristに依存する。
- video gatewayは、libristを安全なRust APIから利用するため、[`comavius/rist-rs`](https://github.com/comavius/rist-rs)に依存する。buildの再現性を確保するため、依存するrevisionを固定する。
- telemetry dataの生成と転送はOpenTelemetry SDKおよびOTLPに依存する。
