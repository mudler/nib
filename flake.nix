{
  description = "nib — a tiny Go LLM agent harness for your terminal";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      # Release targets still supported by nixpkgs unstable.
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "aarch64-darwin"
      ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
      pkgsFor = forAllSystems (system: import nixpkgs { inherit system; });
    in
    {
      packages = forAllSystems (
        system:
        let
          pkgs = pkgsFor.${system};
          nib = pkgs.buildGo126Module {
            pname = "nib";
            version = "0-unstable-${self.shortRev or "dirty"}";
            src = pkgs.lib.cleanSource ./.;
            vendorHash = "sha256-apEBF+/AhaaVykYe8bYBL7JyEjGZWBXplkr9jtjmihw=";

            subPackages = [ "." ];
            # Pure Go: static on Linux; macOS still uses Apple's system libraries.
            env.CGO_ENABLED = 0;
            ldflags = [
              "-s"
              "-w"
              "-X github.com/mudler/nib/internal.Version=0-unstable-${self.shortRev or "dirty"}"
              "-X github.com/mudler/nib/internal.Commit=${self.rev or "dirty"}"
            ];

            preBuild = ''
              cp README.md selfdoc/README.md
            '';

            nativeCheckInputs = [
              pkgs.git
            ]
            ++ pkgs.lib.optionals pkgs.stdenv.hostPlatform.isLinux [ pkgs.procps ];
            # subPackages limits the binary build, but checks cover the whole module.
            checkPhase = ''
              runHook preCheck
              export XDG_CONFIG_HOME="$TMPDIR/nib-test-config"
              mkdir -p "$XDG_CONFIG_HOME"
              go test ./...
              runHook postCheck
            '';

            meta = {
              description = "A tiny, zero-dependency LLM agent harness for your terminal";
              homepage = "https://github.com/mudler/nib";
              license = pkgs.lib.licenses.mit;
              mainProgram = "nib";
              platforms = systems;
            };
          };
        in
        {
          inherit nib;
          default = nib;
        }
      );

      # nix run uses the package's meta.mainProgram; no separate app wrapper needed.
      checks = forAllSystems (system: {
        inherit (self.packages.${system}) nib;
      });

      devShells = forAllSystems (
        system:
        let
          pkgs = pkgsFor.${system};
        in
        {
          default = pkgs.mkShell {
            packages =
              with pkgs;
              [
                go_1_26
                gopls
                gotools
                delve
                git
                gnumake
              ]
              ++ lib.optionals stdenv.hostPlatform.isLinux [ procps ];
            CGO_ENABLED = "0";
            GOTOOLCHAIN = "local";
          };
        }
      );

      formatter = forAllSystems (system: pkgsFor.${system}.nixfmt);
    };
}
