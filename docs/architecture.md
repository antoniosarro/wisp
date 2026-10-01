# Architecture

wisp is a loop that talks to a model, lets it call tools, and shows what
happens. Each part is a small package behind a plain Go interface; there is
no framework around them.

```mermaid
graph TD
    user([you]) --> tui["TUI<br/>internal/tui"]
    user --> shot["one-shot mode<br/>internal/cli"]
    tui --> loop["core loop<br/>internal/core"]
    shot --> loop
    loop --> provider["model provider<br/>internal/model/openaicompat"]
    provider --> endpoint[("OpenAI-compatible endpoint<br/>llama.cpp · vLLM · Ollama · LM Studio · OpenRouter")]
    loop --> dispatch["tool dispatcher<br/>internal/tool"]
    dispatch --> gate["permission gate<br/>internal/permission"]
    gate --> builtin["built-in tools<br/>internal/tool/builtin"]
    gate --> mcp["MCP servers<br/>internal/mcp"]
    gate --> agent["sub-agents<br/>internal/agent"]
    agent -.->|own loop| loop
    loop --> store[("session store<br/>~/.local/share/wisp/session.db")]
    loop --> spans["trace spans<br/>internal/span"]
    spans --> store
    store --> trace["trace page<br/>internal/traceui"]
```

## Packages

| Package | Role |
| --- | --- |
| `cmd/wisp` | The `main` package: just calls `internal/cli` |
| `internal/cli` | Flags, setup, file locations and state, the one-shot mode, project trust, the update check; its end-to-end scenarios are in `testdata/script` |
| `internal/core` | The loop: stream a response, dispatch tool calls, feed results back, repeat until a plain answer. Also the context budget, masking, and compaction ([compaction.md](compaction.md)), and tracing the loop |
| `internal/model` | The provider contract (`Provider`, `Catalog`, `Info`, `Pricing`) |
| `internal/model/openaicompat` | The OpenAI-compatible client: streaming, reasoning, retries, model discovery ([model-provider.md](model-provider.md)) |
| `internal/tool` | The `Tool` interface, registry, concurrent dispatch, and the output budget ([tools.md](tools.md)) |
| `internal/tool/builtin` | read, ls, glob, grep, todo, write, edit, multi_edit, bash, fetch |
| `internal/credential` | Which paths hold credentials, for the tools and the permission rules ([permissions.md](permissions.md#credentials)) |
| `internal/permission` | Approval prompts and session "always allow" rules ([permissions.md](permissions.md)) |
| `internal/agent` | Sub-agents: separate loops behind the `agent` tool ([subagents.md](subagents.md)) |
| `internal/mcp` | MCP clients, `tool_search`, and the `mcp_call` proxy ([mcp.md](mcp.md)) |
| `internal/prompt` | The system prompt: persona, tool and safety rules, environment, `AGENTS.md` |
| `internal/session` | SQLite history, compactions, and spans ([session.md](session.md)) |
| `internal/span` | Timed, nested spans for tracing ([tracing.md](tracing.md)) |
| `internal/traceui` | The `--trace` web page |
| `internal/tui` | The Bubble Tea frontend: the transcript, approvals, commands and pickers, side panels, sub-agent chats, the mouse, and the logo and mascot; its screen snapshots are in `testdata/TestSnapshots` ([usage.md](usage.md)) |
| `internal/termsafe` | Strips escape sequences from model, tool, and endpoint text before it reaches the terminal |
| `internal/tokencount` | Local token estimates (cl100k), calibrated against what the endpoint reports |
| `internal/textfmt` | Cutting text to a size without splitting characters, and byte sizes |
| `internal/version` | wisp's version, set at link time, and its HTTP User-Agent |
| `assets` | Files embedded in the binary: the logo and the mascot's sprite strips, with their Aseprite source (`just sprites` exports them) |
| `internal/testutil` | Fakes for tests: a scripted provider, and (`fakemodel`) a scriptable OpenAI-compatible server for the end-to-end scenarios |

The core loop knows nothing about the terminal, and the TUI holds no agent
logic: it renders what the loop reports and forwards input. The one-shot
mode drives the same loop with a plain-text renderer.

## One turn

```mermaid
sequenceDiagram
    participant U as You
    participant L as Core loop
    participant M as Model endpoint
    participant P as Permission gate
    participant T as Tools
    participant S as Session store

    U->>L: prompt
    L->>S: save the message
    loop until the model answers without tool calls
        L->>L: check the context budget, mask or compact if needed
        L->>M: stream request (history + tool definitions)
        M-->>L: reasoning, text, tool calls
        L->>P: each tool call
        P-->>U: ask, unless read-only or already allowed
        U-->>P: allow / deny / deny with a note
        P->>T: run allowed calls (reads concurrently)
        T-->>L: results, in the order the model asked
        L->>S: save calls and results
    end
    L-->>U: final answer
```

- **Concurrency.** Read-only calls run concurrently. A write or command is
  a barrier: it waits for the calls before it and holds back those after,
  so changes apply in the order the model asked for them.
- **Reasoning** is shown, traced, and counted, but not saved in the
  history or sent back.
- **The step limit.** After 100 round trips without a final answer
  (`--max-iterations`), the model is asked for a summary of its progress
  without tools, and that ends the turn.
- **Tracing.** Every step is also recorded as a span, so `--trace` can
  show it live.

## Scope

- **One level of sub-agents.** Sub-agents can't start their own.
- **Local-first, not a server.** wisp runs where you work, against a local
  or remote model API.
- **Linux first.** The code is portable Go, but only Linux is tested;
  process-group cancellation of shell commands is Linux-specific.
