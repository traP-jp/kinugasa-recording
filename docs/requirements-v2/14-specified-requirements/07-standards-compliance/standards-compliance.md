# 標準への準拠

- cameraクライアントとvideo gateway間の通信はRIST Main Profileに準拠する。
- video gatewayとvideo worker間の通信はRTPに準拠し、MPEG-TSのRTP payload type 33を使用する。
- video gatewayとconsole server間のtelemetry dataの転送はOpenTelemetry ProtocolのgRPC transportに準拠する。
