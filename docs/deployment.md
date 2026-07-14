# k3d deployment on a LAN

S3/LiveKit Secret、build、映像入力、録画、障害復旧を含む全手順は`docs/operations.md`を参照する。

ホストPCとスマートフォンを同じLANへ接続し、ホストPC上で次を実行する。

```sh
nix develop
make k3d-create image-build k3d-import
make deploy
./scripts/k3d-lan-check.sh
```

`make deploy`はdefault routeからホストPCのLAN IPv4 addressを検出し、そのaddressを
camera接続URLとLiveKitの公開URLへ設定する。複数NIC、VPN、default routeがない環境では、
使用するLAN IPv4 addressを明示する。

```sh
PUBLIC_HOST=192.168.1.20 make deploy
PUBLIC_HOST=192.168.1.20 ./scripts/k3d-lan-check.sh
```

ホストPCのfirewallでは次の受信portを同一LANから許可する。

| protocol | port | 用途 |
| --- | ---: | --- |
| TCP | 30080 | Web UI |
| TCP | 30081 | LiveKit signaling |
| TCP | 30082 | LiveKit RTC ServiceのNodePort（診断用。通常は7881を使用） |
| TCP | 7881 | LiveKit RTC fallback |
| UDP | 7882 | LiveKit RTC media |
| UDP | 31000-31099 | cameraのRIST/SRT入力NodePort |

Web UIは`http://<LAN IPv4>:30080`で開く。camera追加後に表示されるRIST/SRT URLは
同じLAN IPv4と、cameraへ割り当てられた`31000-31099`のportを使用する。
`k3d-lan-check.sh`の成功はホスト上の公開経路と設定の一致を示す。スマートフォンと別PCからの
到達性は、LANのclient isolationとホストfirewallの影響を受けるため実機でも確認する。

deploy後の基本flowは次で自動確認できる。このtestは一時S3 mockを使用し、終了時に元のS3設定へ戻す。

```sh
make test-e2e
```
