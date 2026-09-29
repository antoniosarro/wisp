# MCP servers

wisp is an MCP *client*: it connects to MCP servers and exposes their tools
to the model next to the built-in ones.

## Library and transports

- **Library.** The official Go SDK, `github.com/modelcontextprotocol/go-sdk`.
- **Transports.** stdio for local servers, which wisp starts, and
  Streamable HTTP for remote ones. The old HTTP+SSE transport is deprecated
  and not supported.

## Config

The same shape as the `mcpServers` object other clients use, so entries can
be copied across:

- `~/.config/wisp/mcp.json`: global.
- `.wisp/mcp.json`: this project. An entry replaces a global one of the
  same name.

```json
{
  "mcpServers": {
    "aseprite": { "command": "aseprite-mcp", "args": ["--stdio"], "env": { "ASEPRITE": "/usr/bin/aseprite" } },
    "budget":   { "url": "https://budget.example/mcp", "headers": { "Authorization": "Bearer ${BUDGET_TOKEN}" } }
  }
}
```

- **Secrets.** `${VAR}` in `args`, `env`, `url`, and `headers` expands from
  the environment, so secrets stay out of the file.
- **Turning one off.** `"disabled": true` keeps an entry without starting
  it, e.g. to turn a global server off for one project.
- **Environment.** stdio servers run with wisp's environment minus
  `WISP_API_KEY`, plus their `env`.
- **Project trust.** A project's `.wisp/mcp.json` is used only once the
  project is trusted, since it runs commands as you (see
  [permissions.md](permissions.md#project-trust)).

Scope servers per project: a Godot repo doesn't need the budget server, and
every connected server costs startup time and index tokens.

## The cost is context, not the protocol

Servers commonly ship 30–150 tools. Sending every schema on every request
burns tens of thousands of tokens before the user types anything, and most
tools are never called in a given session.

### Deferred tool definitions

Only the built-in tools plus two MCP tools are sent: `tool_search` and
`mcp_call`.

- **The index.** `tool_search`'s description carries it: every MCP tool's
  name (`mcp__<server>__<tool>`) and the first line of its description,
  capped. Each server's `instructions` are appended to its section, also
  capped.
- **The system prompt** names the connected servers and says how to use
  the two tools, since small models otherwise miss the index.

The model calls:

1. `tool_search({"query": "select:mcp__budget__accounts_list,mcp__budget__transactions_filter"})`
   for exact tools, or `tool_search({"query": "transactions by payee"})` for
   a keyword match:
   - It returns at most `max_results`, default 5.
   - A word naming a tool exactly, with or without the `mcp__<server>__`
     prefix, returns just that tool.
   - Words from the server's name are ignored.
   - Name matches rank above description matches.
2. `mcp_call({"name": "mcp__budget__accounts_list", "arguments": {...}})`.

`tool_search` returns each tool's full description and parameter schema as
its result, so the definitions enter the conversation, not the tool list.

### A fixed tool list for the prompt cache

The tool list is part of the cached prompt prefix, and in practice it comes
first: llama.cpp's chat templates render tools into the system prompt, and
hosted APIs put `tools` ahead of the messages. Changing it mid-session
therefore invalidates the *whole* cache, not just what follows the change.

The first design appended loaded schemas to the tool list. Measured on a
six-turn session (spark-4b on llama.cpp, 39 requests), steps with an
unchanged list were 94–99% cached. Every one of the four loads, plus two
resumed turns whose restored list differed, dropped to **0%**, costing 3–4 s
each at 11–15k tokens instead of about 1 s.

So the tool list is fixed for the session, and `mcp_call` proxies to the
MCP tools. The same six prompts then ran with 13 tools on every request:
the only miss was the cold start, and every other step was 93–98% cached.

`mcp_call`:

- **Validates `arguments`** against the target's JSON Schema. An invalid
  call returns the error and the schema, so the model can retry. On the
  provider side, only the proxy's own schema constrains the call.
- **Runs the target behind the target's own permission gate.** Approvals,
  "always allow" rules, and prompts show the MCP tool's name and arguments.
- **Keeps read-only calls parallel.** Calls to read-only tools still run in
  parallel with other calls: the dispatcher asks the proxy per call
  (`tool.CallRisk`).
- **Is bounded** at 10 minutes per call.
- **Accepts any known tool**, named with or without the `mcp__<server>__`
  prefix, even one not searched for.

### Capped output

MCP results go through the same budget as `bash`: output over 30 KB keeps
its head and tail, and the full text is saved to a temporary file the model
can `read` or `grep`.

- **Text** and `structuredContent` are sent as text.
- **`resource_link`s** are sent as their URI, not fetched.
- **Images** are sent as images when the model has vision.

## Approvals

- **Risky by default.** An MCP tool is risky, and goes through the
  permission layer, unless it declares `readOnlyHint: true`.
- **Hints are only hints.** Annotations come from the server, so a
  misbehaving server can mark a writing tool read-only. Only configure
  servers you trust.
- **Destructive tools always ask.** `--dangerously-skip-permissions` still
  asks before destructive MCP tools: per the spec, any tool that isn't
  read-only and doesn't declare `destructiveHint: false`.

## Connecting

- **At startup.** Servers connect before the first request, in parallel,
  with a timeout: the index must be complete in the first prefix. wisp
  prints which servers it is starting and, once they connect, how many
  tools they brought and how long it took. A server that fails to start is
  reported and skipped.
- **Tool list changes.** When a server sends
  `notifications/tools/list_changed`, wisp relists its tools. The index in
  `tool_search`'s description follows from the next request on, a one-time
  prefix change. `mcp_call` reaches new tools at once, and calls to removed
  ones fail as unknown.
- **Resuming** needs nothing special: the definitions the model saw are in
  the history, and the tool list is the same.

## Sub-agents

Sub-agents share the main session's server connections, so no server is
started twice, and their system prompt names the servers. An agent without
a `tools` list gets `tool_search` and `mcp_call`. An agent with a `tools`
list gets them only if it names both.

## MCP servers written for wisp

- **The server's language barely matters.** A JSON-RPC round trip over
  stdio takes microseconds, and the model takes seconds. Write them in
  **Go**: the same language as wisp, the official SDK, a single static
  binary, and easy to package with Nix. Use Rust only for heavy compute
  such as large-scale indexing.
- **For tools only wisp will use, skip MCP.** Implement them as native
  `tool.Tool`s, or use the SDK's in-memory transport. MCP is worth its
  overhead when other clients (Claude Code, Zed, ...) should use the same
  server.
- **Carry wisp-specific hints in `_meta`**: "defer by default", expected
  output size, a cost class, a routing hint. Other clients ignore `_meta`,
  so the server stays standard.
- **Tool design beats raw speed.** Fewer, coarser tools; pagination; a
  `format: concise|detailed` option; IDs plus names instead of full
  objects; and error messages that tell the model what to do next.
