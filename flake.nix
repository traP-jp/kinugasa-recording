{
  description = "kinugasa-recording";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-parts.url = "github:hercules-ci/flake-parts";
  };

  outputs = inputs@{flake-parts, ...}:
    flake-parts.lib.mkFlake {inherit inputs;} {
      systems = [
        "aarch64-darwin"
        "aarch64-linux"
        "x86_64-darwin"
        "x86_64-linux"
      ];

      perSystem = {pkgs, ...}: {
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            buf
            ffmpeg
            go
            golangci-lint
            gopls
            kubectl
            librist
            mediamtx
            nodejs
            pnpm
            postgresql
            protobuf
            protoc-gen-go
            protoc-gen-go-grpc
          ];
        };
      };
    };
}
