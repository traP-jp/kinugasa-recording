# ソフトウェアシステム属性

## 可用性

- telemetry経路の障害はmedia経路から分離し、console serverまたはOTLP経路の停止中もvideo gatewayはRISTの受信とvideo workerへのRTP送信を継続する。
- console serverの再起動後は、新しいOpenTelemetry Metricsを受信することでRIST統計の表示を自動的に再開する。

## 観測可能性

- RIST統計には、どのSession、camera、gateway instanceおよびRIST flowに対する観測かを識別できる情報を含める。
- RIST回復後packet lossとgatewayからvideo workerまでのpacket lossは異なる観測境界とし、同じ指標として扱ってはならない。
- telemetry dataの不在を0% packet lossとして表示してはならない。
