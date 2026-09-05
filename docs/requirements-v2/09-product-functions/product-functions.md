# 製品機能

## RIST通信品質の観測

- video gatewayは、RISTのARQと並べ替えの後に出力されるpacketのsequence numberをflowごとに観測する。
- video gatewayは、復旧後出力packet数、RIST回復後packet loss数、回復packet数および不連続回数をOpenTelemetry Metricsとしてconsole serverへ逐次送信する。
- console serverは、受信したRIST統計をweb consoleへ提供する。RIST回復後packet loss率はクラスタ内で受け渡さない。
- web consoleは、cameraごとのRIST回復後packet loss率を各5秒区間の復旧後出力packet数とRIST回復後packet loss数から算出し、その区間のpacket数および最終観測時刻とともに逐次表示する。
