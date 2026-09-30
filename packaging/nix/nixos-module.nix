# NixOS: installs wisp for every user. Per-user settings (endpoint, MCP
# servers, agents) belong in the home-manager module.
self:
{
  config,
  lib,
  pkgs,
  ...
}:

let
  cfg = config.programs.wisp;
in
{
  options.programs.wisp = {
    enable = lib.mkEnableOption "wisp, a terminal agent harness";
    package = lib.mkOption {
      type = lib.types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
      defaultText = lib.literalExpression "wisp.packages.\${system}.default";
      description = "The wisp package to install.";
    };
  };

  config = lib.mkIf cfg.enable {
    environment.systemPackages = [ cfg.package ];
  };
}
