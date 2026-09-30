{
  lib,
  buildGoModule,
  version ? "0.0.0-dev",
}:

buildGoModule {
  pname = "wisp";
  inherit version;

  # Only what the build reads, so editing docs or packaging doesn't rebuild.
  src = lib.fileset.toSource {
    root = ../..;
    fileset = lib.fileset.unions [
      ../../go.mod
      ../../go.sum
      ../../cmd
      ../../internal
      ../../assets
    ];
  };

  # Update after changing go.mod: set lib.fakeHash, build, copy the "got:" hash.
  vendorHash = "sha256-qYlbjVvchGUjlolDmDMFy0NZkhMV1w6kmmuy9gOgQig=";

  subPackages = [ "cmd/wisp" ];
  env.CGO_ENABLED = 0; # modernc SQLite is pure Go: a static binary
  ldflags = [
    "-s"
    "-w"
    "-X github.com/antoniosarro/wisp/internal/version.Version=${version}"
  ];

  # The TUI tests drive a pseudo-terminal and time its reads, which the
  # build sandbox makes unreliable; `just test` and `just race` run them.
  doCheck = false;

  meta = {
    description = "Small agent harness with a terminal UI, built-in tools, and resumable sessions";
    license = lib.licenses.mit;
    homepage = "https://github.com/antoniosarro/wisp";
    mainProgram = "wisp";
    platforms = lib.platforms.linux ++ lib.platforms.darwin;
  };
}
