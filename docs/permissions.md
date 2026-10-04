# Permissions

What asks the user before it runs, how an answer can cover later calls,
and what a cloned project may configure before it is trusted.

## What asks

- **Risky tools ask every time**, unless the user allowed matching calls
  earlier in the session. They are `write`, `edit`, `multi_edit`, `bash`,
  `fetch`, and MCP tools not marked read-only (see
  [mcp.md](mcp.md#approvals)).
- **`read` and `grep` ask only for credentials** (below).
- **Everything else runs without asking**: `ls`, `glob`, `todo`,
  `tool_search`, and the `agent` tool itself, whose sub-agent's calls ask
  on their own. `mcp_call` asks when the MCP tool it calls would.

## Answering

The TUI prompt shows its keys, with the "always" scope named:

```
y allow   a always allow git status commands   n deny   t deny with note
esc cancel turn
```

One-shot runs ask on the terminal: `[y] allow  [a] always allow ...  [N] deny`.

- **`t` denies with a note** ("use pnpm instead"). The model receives the
  note in the tool result.
- **Keys count only after a pause.** In the TUI, an answer key counts only
  after the prompt has been visible and typing has paused for 0.4 s. A
  prompt that appears while you type a draft can't take a letter as an
  answer.
- **What runs is what you see.** The approval box and the terminal prompt
  show control characters in arguments visibly (`␛`, `␍`) rather than
  letting the terminal act on them. The TUI box says how many lines are out
  of view.
- **Writes say where they land.** `write`, `edit`, and `multi_edit` follow
  symlinks, replacing the target. When the path is a link, the prompt names
  the target too: the TUI box as `notes.txt → ~/.bashrc`, the terminal
  prompt of a one-shot run on a line of its own. A link in a repository
  can't pass a write to another file off as a write to itself.

## "Always allow"

`a` is remembered for the rest of the session only; nothing is written to
disk. What it covers is decided by `permission.RuleKey`:

- **`bash`:**
  - **Only the exact command** when the command has `;`, `&`, `|`,
    redirects, `$`, backticks, parentheses, or a control character (a
    tab aside), starts with `VAR=`, or names its program by path (`./git`,
    `/usr/bin/git`).
  - **The whole program** for programs that only list or report (`ls`,
    `wc`, `pwd`, `echo`, `stat`, `du`, `ps`, ...): `ls -la` covers every
    `ls` command.
  - **The whole program, minus credentials,** for programs that print what
    files contain (`cat`, `head`, `tail`, `grep`, `diff`, `cmp`, `cut`,
    `jq`). `cat notes.txt` covers `cat` commands, but a command that could
    print a credential covers only itself, and one run later asks again.
    That is a command with an argument that:
    - names a credential path (`~` expanded, `--opt=value` values
      included; quotes and backslash escapes resolved, so `cat .e'nv'`
      counts as `.env`),
    - needs shell expansion only the shell can resolve (a `$`, backtick,
      or brace alternative), since then what it names isn't knowable,
    - is a glob,
    - or is a directory;

    or, for `grep`, one that searches recursively. So "always allow" on
    `cat notes.txt` can't let `cat ~/.ssh/id_rsa` through, where `read`
    would ask.
  - **One subcommand** for programs that dispatch on one (`git`, `go`,
    `cargo`, `npm`, `pnpm`, `yarn`, `docker`, `podman`, `kubectl`, `gh`,
    `just`, `make`, `systemctl`, `uv`, `pip`, ...): `git status` covers
    `git status` commands, not `git push`. The subcommand is read as the
    shell passes it, so `go "run"` covers only itself, like `go run`.
    - **Exception:** subcommands that run code or change configuration
      cover only the exact command. These are `run`, `exec`, `x`, `dlx`,
      `create`, `init`, `install`, `i`, `add`, `generate`, `tool`,
      `config`, `env`, `api`, `eval`, `shell`, `cp`, `attach`, and `debug`.
  - **Only the exact command** for every other program.
- **`fetch`:** the URL's host, as parsed (`https://docs.x@evil.com/` is
  `evil.com`).
- **`write`, `edit`, `multi_edit`:**
  - **Inside the working directory**, every path, except under `.git/` or
    `.wisp/` (hooks, config, MCP servers, agents), where each file asks on
    its own.
  - **Outside it**, only that file.
  - **Symlinks** are resolved before the check, so a link inside the
    directory that points out of it counts as outside.
- **`read`, `grep`:** only the one credential file or directory asked
  about.
- **Other tools:** the whole tool.

Sub-agents share these rules with the main agent (see
[subagents.md](subagents.md#running)).

## Credentials

Reading never asks, and what the model reads could leave through an
approved `fetch` or command. So `read` and `grep` ask before reading
credentials:

- **Directories** under the home directory: `~/.ssh`, `~/.gnupg`, `~/.aws`,
  `~/.azure`, `~/.kube`, `~/.docker`, `~/.password-store`,
  `~/.config/gcloud`, `~/.config/gh`, and `~/.local/share/keyrings`.
- **Files** anywhere, by name: `id_rsa*`, `id_dsa*`, `id_ecdsa*`,
  `id_ed25519*`, `*.pem`, `*.key`, `*.p12`, `*.pfx`, `*.kdbx`, `.netrc`,
  `.pgpass`, `.env`, and `.env.*`. Names ending in `.example`, `.sample`,
  or `.template` hold placeholders and don't count.
- **Symlinks** are followed, so a link to a key counts as the key.
- **Wider searches** don't ask, and don't need to. Walks (`glob`, `grep`)
  skip credential directories, and `grep` skips credential files it meets,
  saying how many. A search of the project can't return a `.env`.

The lists are in `internal/credential`, shared by the tools and the rules
above. Symlinks are resolved through the longest existing part of a path,
so a link to a key yet to be created, or a home directory behind a symlink,
still counts.

## Skipping approvals

`--dangerously-skip-permissions` runs risky calls without asking, with one
exception: it still asks before destructive MCP tools (see
[mcp.md](mcp.md#approvals)). A small model that loads the wrong tool can't
close an account unasked.

## Project trust

A cloned repository's `.wisp/mcp.json` would otherwise run commands as you
the moment wisp starts, and its agent files could send your key to an
endpoint they name. So a project's `.wisp/mcp.json`, and the endpoints and
keys named in `.wisp/agents/*.md`, are used only once the project is
trusted.

- **Asking.** wisp asks once, in the terminal, before starting. It lists
  each MCP server's command line or URL, and each agent's endpoint and key
  variable.
  - Everything listed comes from the repository, so control characters
    are shown (`␛`), not acted on: an escape sequence could otherwise
    redraw the lines being reviewed.
  - The answer is read a byte at a time, up to the end of its line, so
    what follows on stdin is left for the next prompt.
- **Remembering.**
  - A "yes" is remembered in `$XDG_STATE_HOME/wisp/state.json`, keyed by
    the project directory, with a hash of those files' names and contents.
    Any change asks again.
  - A "no" is not remembered.
- **Without a terminal** (stdin and stderr), the project config is ignored
  with a notice unless `--trust-project` is given, which trusts it and
  remembers that.
- **While untrusted:**
  - The project's MCP servers aren't started.
  - Its agents still load, but without their `base_url` and
    `api_key_env`, so they run on the main endpoint.
- **Global config** in `~/.config/wisp` is always used.
