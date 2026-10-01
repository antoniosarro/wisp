{
  description = "wisp - minimal modular agent harness";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
    }:
    let
      # Packaging lives in packaging/ (packaging/README.md). Flakes can't see
      # git tags, so the version comes from VERSION, which scripts/release.sh
      # sets in the commit it tags.
      version = nixpkgs.lib.trim (builtins.readFile ./VERSION);
      wisp = pkgs: pkgs.callPackage ./packaging/nix/package.nix { inherit version; };
    in
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
      in
      {
        formatter = pkgs.nixfmt;

        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            golangci-lint
            gotools
            just
            nfpm # .deb and Arch packages: just pkg deb, just pkg archlinux
            sway # a private headless display for scripts/screenshot.sh, where kitty shows real graphics
            grim # screenshots that display for visual TUI QA
            wtype # types into it
            jq # formats test results (scripts/test.jq)
            wf-recorder # records it for GIFs in the docs (scripts/screenshot.sh)
            ffmpeg-headless # splits the recording into frames
            gifski # encodes the frames as a small, sharp GIF
            sqlite # scripts/screenshot.sh reads the session's spans to know when a turn ends
          ];
        };

        packages.default = wisp pkgs;
      }
    )
    // {
      overlays.default = final: _: { wisp = wisp final; };
      nixosModules.default = import ./packaging/nix/nixos-module.nix self;
      homeManagerModules.default = import ./packaging/nix/hm-module.nix self;
    };
}
