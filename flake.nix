{
  description = "🎣 Bait: Almost Idiomatic Transpiler";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
  };

  outputs = { nixpkgs, ... }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "aarch64-darwin"
      ];
      forAllSystems = f:
        nixpkgs.lib.genAttrs systems
          (system: f nixpkgs.legacyPackages.${system});

      devTools = pkgs: [
        pkgs.bash
        pkgs.fd
        pkgs.fish
        pkgs.go
        pkgs.goreleaser
      ];

      mkTask = pkgs: name: text:
        pkgs.writeShellApplication {
          inherit name text;
          runtimeInputs = devTools pkgs;
        };

      testRunner = pkgs:
        mkTask pkgs "bait-test" ''
          echo "==> Running internal unit and equivalence tests..."
          go test -v ./internal/bait ./cmd/bait
          echo "==> Running e2e sandbox tests..."
          go test -v ./e2e
        '';

      fmtRunner = pkgs:
        mkTask pkgs "bait-fmt" ''
          echo "==> Formatting Go files..."
          go fmt ./...
          echo "==> Formatting Fish files..."
          fd --extension fish --exec-batch fish_indent --write
        '';
    in
    {
      packages = forAllSystems (pkgs: rec {
        bait = pkgs.buildGoModule {
          pname = "bait";
          version = "0.3.0";
          src = ./.;
          subPackages = [ "cmd/bait" ];
          vendorHash = "sha256-tCFu9E2pFBWBQFiRVvI16FNI3dE1bUKJlsEbvDAo7lo=";
        };
        test = testRunner pkgs;
        fmt = fmtRunner pkgs;
        default = bait;
      });

      formatter = forAllSystems (pkgs: fmtRunner pkgs);

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = devTools pkgs;
        };
      });
    };
}
