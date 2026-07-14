# kinugasa-recording

複数cameraからのRIST/SRT映像をpreview・分割録画し、録画fileをS3互換object storageへ逐次uploadするKubernetes Operatorである。

## Development

開発toolchainは`flake.nix`で固定する。

```console
nix develop
pnpm install --frozen-lockfile
make all
```

利用可能な共通entry pointは`make help`で確認する。要件は`docs/requirements.md`、
開発・deploy・運用手順は`docs/operations.md`、受け入れ状況は`docs/acceptance.md`、
未完了作業は`docs/todo.md`を参照する。
