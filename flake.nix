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
            exec fish ${./scripts/test.fish} "$@"
          '';
        };

      fmtRunner = pkgs:
        pkgs.writeShellApplication {
          name = "bait-fmt";
          runtimeInputs = with pkgs; [
            fd
            fish
            go
          ];
          text = ''
            exec fish ${./scripts/fmt.fish} "$@"
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
        fmt = fmtRunner pkgs;
        default = bait;
      });

      checks = forAllSystems (pkgs: {
        test = pkgs.buildGoModule {
          pname = "bait-test";
          version = "0.1.0";
          src = ./.;
          subPackages = [ "cmd/bait" ];
          vendorHash = "sha256-tCFu9E2pFBWBQFiRVvI16FNI3dE1bUKJlsEbvDAo7lo=";
          nativeCheckInputs = with pkgs; [
            bash
            fish
            which
          ];
          checkPhase = ''
            export HOME=$(mktemp -d)
            fish ./scripts/test.fish ./internal/bait ./cmd/bait
          '';
          installPhase = ''
            touch $out
          '';
        };
      });

      formatter = forAllSystems (pkgs: fmtRunner pkgs);

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          # Target shells and tools used for development and test execution.
          packages = with pkgs; [
            bash
            fish
            go
            goreleaser
            (testRunner pkgs)
            (fmtRunner pkgs)
          ];
        };
      });
    };
}
