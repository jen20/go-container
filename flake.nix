{
  description = "go-container Development Environment";

  # The go-overlay CI pushes builds of the Go toolchain and its tools to
  # go-overlay.cachix.org, built against its own nixpkgs. This flake pins
  # go-overlay to a commit whose builds are in that cache, and uses the
  # same nixpkgs, so the toolchain downloads instead of compiling. Before
  # you move the pin, make sure that the new commit's builds are cached.
  nixConfig = {
    extra-substituters = [ "https://go-overlay.cachix.org" ];
    extra-trusted-public-keys = [
      "go-overlay.cachix.org-1:rJ155O3K6WUyUcoKE4MqcC6JcLlK2vPXnP5/76BxWD8="
    ];
  };

  inputs = {
    go-overlay.url = "github:purpleclay/go-overlay/515f51ff72d899f26a235c17c5009a5e297d0962";
    nixpkgs.follows = "go-overlay/nixpkgs";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
      go-overlay,
      ...
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs {
          inherit system;
          overlays = [ go-overlay.overlays.default ];
        };

        go = pkgs.go-bin.fromGoMod ./go.mod;
      in
      {
        devShells.default = pkgs.mkShell {
          buildInputs = [
            # Go toolchain (version per go.mod file)
            go.withDefaultTools

            # Pre-commit lint runner
            pkgs.prek

            # Shell script linter, for the prek hook
            pkgs.shellcheck
          ];

          shellHook = ''
            # Install the prek Git shim if it is not there yet.
            if hooks_dir=$(git rev-parse --git-path hooks 2>/dev/null) \
              && ! grep -qs prek "$hooks_dir/pre-commit"; then
              prek install --config prek.toml \
                || echo "Could not install the prek hooks; run 'prek install --config prek.toml' to see why." >&2
            fi
          '';
        };
      }
    );
}
