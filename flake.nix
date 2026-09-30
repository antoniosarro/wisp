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
      # Packaging lives in packaging/ (packaging/README.md).
      version = "0.0.0-" + (self.shortRev or self.dirtyShortRev or "dev");
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
            jq
            nfpm # .deb and Arch packages: just pkg deb, just pkg archlinux
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
