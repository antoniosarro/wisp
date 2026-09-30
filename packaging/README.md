# Packaging

Everything that turns wisp into something to install. Recipes live in
`packaging/justfile`, a module of the root justfile: run them from the
repository root as `just pkg <recipe>` (`just pkg` lists them). Artifacts
go to `dist/`, which git ignores. Run them in the dev shell (`nix develop`,
or direnv), which has Go, just, and nfpm.

```
packaging/
├── justfile            recipes: binary, deb, archlinux, all, aur, nix, version, clean
├── nfpm.yaml           the .deb and Arch package, from the static binary
├── arch/PKGBUILD       an Arch source build, for makepkg and later the AUR
└── nix/
    ├── package.nix     the derivation (buildGoModule)
    ├── nixos-module.nix
    └── hm-module.nix   home-manager
```

wisp is one static binary: SQLite is modernc's pure-Go port, so it builds
with `CGO_ENABLED=0` and runs on any Linux of its architecture without
libraries. The Linux packages only place that binary (and the README).

## Version

`just pkg version` prints what packages get, from git: `1.2.3` at the tag
`v1.2.3`, `1.2.3.r4.gabc1234` four commits past it, and
`0.0.0.r<commits>.g<hash>` before the first tag. Deb, Arch, and Nix order
these correctly. The binary reports it: `wisp --version`. The version is
set at link time (`-ldflags "-X github.com/antoniosarro/wisp/internal/version.Version=..."`, in `internal/version`).
Without it, wisp uses the version Go records: `1.2.3` from
`go install github.com/antoniosarro/wisp/cmd/wisp@v1.2.3`, a pseudo-version
such as `0.0.0-20260929083344-013debf7a7df` from `go build` in a checkout,
and `dev` from `go run`.

To release, run `just release` (`scripts/release.sh`): wisp, on a local
model, reads the changes since the last tag and suggests the version and
the notes; you confirm, and the script tags and pushes, asking before each
step. By hand:

```sh
git tag -a v0.1.0 -m "release notes" && git push origin HEAD v0.1.0
```

The pushed tag runs `.github/workflows/release.yml`: it builds the .deb
and Arch packages at the tag and publishes the GitHub release, with the
tag's message as the notes.

On launch, wisp asks GitHub for the newest release
(`api.github.com/repos/antoniosarro/wisp/releases/latest`, at most once a
day, in the background) and, if it is newer, shows "Update available" on
the splash. Only builds made at or past a tag check: `dev`, pseudo-versions
before the first tag, Nix builds
(`0.0.0-<rev>`: flakes can't see tags, and Nix updates come from
`nix flake update`), and packages from before the first tag never do. A
tag alone isn't a release: the notice appears once the release is
published.

## Nix and NixOS

The flake at the repository root exposes the package, an overlay, and the
modules:

| Output | |
|---|---|
| `packages.<system>.default` | wisp; `nix build`, `nix run`, `nix profile install` |
| `overlays.default` | adds `pkgs.wisp` |
| `nixosModules.default` | `programs.wisp.enable`: installs it for every user |
| `homeManagerModules.default` | `programs.wisp`: installs it and writes `~/.config/wisp` |

```sh
nix run .                      # try it
just pkg nix                   # build ./result
```

In a system flake:

```nix
inputs.wisp.url = "github:antoniosarro/wisp"; # or path:/path/to/wisp before it is public

# NixOS
imports = [ inputs.wisp.nixosModules.default ];
programs.wisp.enable = true;

# home-manager
imports = [ inputs.wisp.homeManagerModules.default ];
programs.wisp = {
  enable = true;
  baseUrl = "https://openrouter.ai/api/v1";   # WISP_BASE_URL
  model = "z-ai/glm-5.3-flash";               # WISP_MODEL
  apiKeyFile = "/run/agenix/openrouter-key";  # read at launch, never in the store
  mcpServers.fs = { command = "npx"; args = [ "-y" "@modelcontextprotocol/server-filesystem" "/tmp" ]; };
  agents.reviewer = ./agents/reviewer.md;     # or the Markdown as a string
};
```

`baseUrl`, `model`, and `apiKeyFile` go through a wrapper as defaults: a
variable set in the shell, or a flag, still wins. `mcpServers` becomes
`~/.config/wisp/mcp.json`, `agents` become `~/.config/wisp/agents/*.md`:
the global config, used in every project without the trust prompt
(docs/mcp.md, docs/subagents.md).

After changing Go dependencies, update `vendorHash` in `nix/package.nix`:
set it to `lib.fakeHash`, run `just pkg nix`, and copy the hash the error
reports. New files must be known to git (`git add`, or `git add -N`)
before Nix sees them.

## Ubuntu and Debian

```sh
just pkg deb                   # dist/wisp_<version>_amd64.deb
just pkg deb arm64
sudo apt install ./dist/wisp_*_amd64.deb
```

Built anywhere, NixOS included: nfpm writes the package without dpkg. It
suggests git, which the prompt uses for the branch name.

## Arch

Two ways:

```sh
just pkg archlinux             # dist/wisp-<version>-1-x86_64.pkg.tar.zst, built anywhere
sudo pacman -U dist/wisp-*-x86_64.pkg.tar.zst
```

or build from source on Arch itself, with the PKGBUILD:

```sh
just pkg aur                   # dist/aur/: PKGBUILD + a tarball of HEAD (commit first)
cd dist/aur && makepkg -si
```

The PKGBUILD needs Go at least as new as `go.mod`'s (1.26.7): Arch's Go
builds with `GOTOOLCHAIN=local`, so it won't download a newer toolchain.
For the AUR, once github.com/antoniosarro/wisp is public, switch `source`
to the release tarball (the commented line in the PKGBUILD).

## Later: macOS and Windows

The binary cross-compiles as it is: `GOOS=darwin` or `GOOS=windows`
with `GOARCH=amd64|arm64`, still with `CGO_ENABLED=0`. What differs is
delivery:

- **macOS**: a Homebrew formula or tap (the usual route), or a tarball.
  The Nix package already lists darwin platforms, so `nix run` works there.
  Unsigned binaries downloaded by a browser get quarantined; installed
  through brew or nix they don't.
- **Windows**: Scoop and winget are the native routes, each a small
  manifest pointing at a zip of `wisp.exe`. The `npx` route that Node
  tools use works for Go binaries too: an npm package per platform, each
  holding one binary (`@wisp/win32-x64`, ...), and a main package listing
  them as `optionalDependencies` with a tiny launcher that runs the
  right one, as esbuild and Biome do. `npx wisp` then downloads only
  the binary for that machine.
- Linux-only parts to check first: the bash tool (`bash -c`, process
  groups in `process_linux.go`), and the TUI's terminal handling on
  Windows consoles.

GoReleaser can produce all of these (archives, Homebrew, Scoop, winget,
nfpm packages, AUR) from one config, if hand-written recipes get to be too
many; nfpm here is the same library it uses.
