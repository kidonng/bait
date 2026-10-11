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
        pkgs.just
      ];
    in
    {
      packages = forAllSystems (pkgs: {
        default = pkgs.buildGoModule {
          pname = "bait";
          version = "0.6.0";
          src = ./.;
          subPackages = [ "cmd/bait" ];
          vendorHash = "sha256-CHWW6PxHK1O/UDuGkr1cM+KKC9BaK/MceF2tTrSE/iE=";
        };
      });

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShellNoCC {
          packages = devTools pkgs;
        };
      });
    };
}
