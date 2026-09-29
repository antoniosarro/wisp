# Using wisp

Run wisp from the directory you want to work in. Without a prompt it opens
the terminal UI; with one, it runs a single turn and exits.

```sh
wisp                                 # the TUI
wisp "Explain this repository"       # one turn, printed to stdout
wisp --resume 3f2a                   # continue a session (an id prefix works in the TUI)
```

## The TUI

The splash shows the model, directory, session, and endpoint. Type a prompt
and press Enter. The answer streams in; reasoning, when the model shows it,
appears in a dimmed box above it, and each tool call gets a card that fills
in as it finishes. The composer stays editable while the model works, so
you can write the next message.

### Keys

| Key | Action |
| --- | --- |
| Enter | Send; while the model works, keep the draft |
| Alt+Enter or Ctrl+J | New line |
| Up / Down | Recall sent prompts (inside a multi-line draft, move first) |
| Esc or Ctrl+C | Cancel the running turn |
| Esc (idle) | Close help or a list, leave a sub-agent chat, clear the selection |
| Ctrl+C (idle) | Clear the input; twice to exit |
| PgUp / PgDown, Ctrl+U / Ctrl+D, wheel | Scroll |
| Ctrl+End | Jump to the latest output and follow it |
| Alt+Left / Alt+Right | Select the previous / next block |
| Ctrl+O | Expand or collapse the selected tool output or reasoning |
| Ctrl+Y | Copy the selected block, full tool output included |
| Ctrl+R | Toggle the latest reasoning |
| → (empty input) | Accept the suggested next message (`--suggest`) |
| Ctrl+B | Back from a sub-agent chat |
| F1 | Help |

The mouse works too: hover shows what a click hits, clicking a tool or
reasoning box expands it, and dragging copies text. Some terminals don't
send Alt+Enter; Ctrl+J always works.

### Commands

| Command | Action |
| --- | --- |
| `/help` | Keys and commands |
| `/model`, `/model NAME` | Pick a model from the endpoint's list, with context and capabilities, or switch |
| `/sessions`, `/resume`, `/resume ID` | List this directory's sessions, or switch to one while idle |
| `/clear` | Start a new session; the current one stays saved |
| `/context` | What fills the context window, by category |
| `/compact [focus]` | Summarize the conversation now, optionally saying what to keep |
| `/debug` | Token usage, timing, cost, and the context budget |
| `/todo`, `/agents` | Show or hide the task list and the sub-agent panel |
| `/back` | Back from a sub-agent chat |

Typing `/` lists the commands, and after `/model` or `/resume` their
choices: type to filter, ↑/↓ to choose, Tab to complete, Enter to run.

On a narrow terminal, `/debug` replaces the transcript; `/debug` again
returns to it. The smallest usable size is 20 × 8.

## Approvals

Reading and searching run without asking. Writing files, editing, running
commands, fetching URLs, and MCP tools that aren't read-only ask first.
The prompt shows the command and its directory, the proposed edit as a
diff, or the new file's content:

| Key | Answer |
| --- | --- |
| `y` | Allow this call |
| `a` | Always allow calls like it, for the rest of the session |
| `n` | Deny |
| `t` | Deny with a note that tells the model what to do instead |
| Esc | Cancel the whole turn |

- **"Calls like it"** depends on the tool: `git status` covers `git
  status` commands but not `git push`, `fetch` covers the host, and edits
  cover the working directory. [permissions.md](permissions.md) has the
  details.
- **Keys count only after a pause.** An answer key counts once the prompt
  has been visible and you have paused typing for 0.4 s, so a prompt that
  appears mid-sentence can't take a letter as an answer.
- **Approval isn't a sandbox:** tools run with your account's access.

## One-shot mode

```sh
wisp "Add a test for parsePrice"
wisp --model qwen3-coder "Explain this repository"
```

- **Output.** The answer, dimmed reasoning, and tool calls print as plain
  text as they stream, with escape sequences stripped. On stderr, the
  session id is printed when the run starts, and a usage summary
  (requests, tokens, cache hits, cost) when it ends.
- **Approvals** are asked on the terminal: `[y] allow  [a] always allow ...
  [N] deny`. `--dangerously-skip-permissions` runs without asking, for
  scripts and benchmarks in a sandbox.
- **Exit status.** Interrupted with Ctrl+C, wisp exits with status 130; a
  failed turn exits with 1.
- **No model given?** wisp picks the one last used with the endpoint, or
  its only model. When several remain, it lists them and exits.

## Sessions

Every conversation is saved in `~/.local/share/wisp/session.db`, tagged
with the working directory: messages, tool calls and results,
compactions, and trace spans.

- **Listing and resuming.** `wisp --sessions` or `/sessions` lists recent
  sessions, and `--resume ID` or `/resume` continues one, with its model.
- **Repairs.** A turn interrupted by Esc or a crash is repaired on resume,
  so strict endpoints accept the history.
- **Sharing.** Several wisp processes can share a directory.
- **Not saved:** reasoning and images.

See [session.md](session.md) for the schema and [tracing.md](tracing.md)
for browsing sessions as timelines.

## Long conversations

When the context window fills up, wisp first replaces old tool output with
short stand-ins that say how to get it back, and, if that isn't enough,
summarizes the older part of the conversation while keeping the recent part
verbatim. With a local model the summary is prepared between turns, so it
costs no wait. A notice in the transcript marks each compaction and expands
to show the summary. See [compaction.md](compaction.md).
