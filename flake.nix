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

      testRunner = pkgs:
        pkgs.writeShellApplication {
          name = "bait-test";
          runtimeInputs = with pkgs; [
            bash
            fish
            go
          ];
          text = ''
            if [ "$#" -eq 0 ]; then
              echo "==> Running internal unit and equivalence tests..."
              go test -v ./internal/bait ./cmd/bait
              echo "==> Running e2e sandbox tests..."
              go test -v ./e2e
            else
              go test -v "$@"
            fi
          '';
        };
    in
    {
      packages = forAllSystems (pkgs: rec {
        bait = pkgs.buildGoModule {
          pname = "bait";
          version = "0.1.0";
          src = ./.;
          subPackages = [ "cmd/bait" ];
          vendorHash = "sha256-tCFu9E2pFBWBQFiRVvI16FNI3dE1bUKJlsEbvDAo7lo=";
        };
        test = testRunner pkgs;
        default = bait;
      });

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          # Target shells and tools used for development and test execution.
          packages = with pkgs; [
            bash
            fish
            go
            goreleaser
            (testRunner pkgs)
          ];
        };
      });
    };
}
