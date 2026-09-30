# home-manager: installs wisp and writes its per-user configuration, the
# files it reads from ~/.config/wisp (docs/mcp.md, docs/subagents.md).
self:
{
  config,
  lib,
  pkgs,
  ...
}:

let
  cfg = config.programs.wisp;
  json = pkgs.formats.json { };

  env = lib.filterAttrs (_: v: v != null) {
    WISP_BASE_URL = cfg.baseUrl;
    WISP_MODEL = cfg.model;
  };

  # Defaults only: a variable set in the shell, or a flag, still wins. The
  # key is read when wisp starts, so it never enters the Nix store.
  wrapped =
    if env == { } && cfg.apiKeyFile == null then
      cfg.package
    else
      pkgs.symlinkJoin {
        name = "wisp-${cfg.package.version}";
        paths = [ cfg.package ];
        nativeBuildInputs = [ pkgs.makeWrapper ];
        postBuild = ''
          wrapProgram $out/bin/wisp \
            ${lib.concatStringsSep " " (lib.mapAttrsToList (k: v: "--set-default ${k} ${lib.escapeShellArg v}") env)} \
            ${lib.optionalString (cfg.apiKeyFile != null) "--run ${lib.escapeShellArg ''[ -n "''${WISP_API_KEY-}" ] || export WISP_API_KEY="$(< ${lib.escapeShellArg cfg.apiKeyFile})"''}"}
        '';
        inherit (cfg.package) meta;
      };
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

    baseUrl = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      example = "http://localhost:8000/v1";
      description = "OpenAI-compatible endpoint, as WISP_BASE_URL.";
    };

    model = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      example = "qwen3-coder";
      description = "Model to use, as WISP_MODEL. Unset, wisp picks the last one used or asks.";
    };

    apiKeyFile = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      example = "/run/agenix/openrouter-key";
      description = ''
        File holding the API key, read into WISP_API_KEY each time wisp
        starts unless that is already set. A path as a string, so the
        secret stays out of the Nix store: e.g. an agenix or sops-nix secret.
      '';
    };

    mcpServers = lib.mkOption {
      inherit (json) type;
      default = { };
      example = lib.literalExpression ''
        {
          github = { url = "https://api.githubcopilot.com/mcp/"; headers.Authorization = "Bearer ''${GITHUB_TOKEN}"; };
          fs = { command = "npx"; args = [ "-y" "@modelcontextprotocol/server-filesystem" "/tmp" ]; };
        }
      '';
      description = "MCP servers for every project, written to ~/.config/wisp/mcp.json (docs/mcp.md).";
    };

    agents = lib.mkOption {
      type = lib.types.attrsOf (lib.types.either lib.types.path lib.types.lines);
      default = { };
      example = lib.literalExpression ''
        {
          reviewer = '''
            ---
            name: reviewer
            description: Reviews a diff for bugs
            tools: [read, grep, glob]
            ---
            Review the change you are given and report bugs with file:line.
          ''';
        }
      '';
      description = "Sub-agents for every project, by name: Markdown with YAML front matter, written to ~/.config/wisp/agents/<name>.md (docs/subagents.md).";
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages = [ wrapped ];

    xdg.configFile = lib.mkMerge [
      (lib.mkIf (cfg.mcpServers != { }) {
        "wisp/mcp.json".source = json.generate "wisp-mcp.json" { inherit (cfg) mcpServers; };
      })
      (lib.mapAttrs' (
        name: agent:
        lib.nameValuePair "wisp/agents/${name}.md" (
          if builtins.isPath agent then { source = agent; } else { text = agent; }
        )
      ) cfg.agents)
    ];
  };
}
