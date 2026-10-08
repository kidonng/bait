{
  description = "🐛 Bait: Almost Idiomatic Transpiler";

  inputs = {
    nixpkgs.url = "https://flakehub.com/f/DeterminateSystems/nixpkgs-weekly/0.1";
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
    in
    {
      packages = forAllSystems (pkgs:
        let
          task = name: text:
            pkgs.writeShellApplication {
              inherit name text;
              runtimeInputs = devTools pkgs;
            };
        in
        rec {
          default = pkgs.buildGoModule {
            pname = "bait";
            version = "0.5.0";
            src = ./.;
            subPackages = [ "cmd/bait" ];
            vendorHash = "sha256-CHWW6PxHK1O/UDuGkr1cM+KKC9BaK/MceF2tTrSE/iE=";
          };

          test = task "test" ''
            echo "==> Running internal unit and equivalence tests..."
            go test ./internal/bait ./cmd/bait
          '';

          e2e = task "e2e" ''
            echo "==> Running e2e sandbox tests..."
            go test ./e2e
          '';

          fmt = task "fmt" ''
            echo "==> Formatting Go files..."
            go fmt ./...
            echo "==> Formatting Fish files..."
            fd --extension fish --exec-batch fish_indent --write
          '';
        }
      );

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = devTools pkgs;
        };
      });
    };
}
