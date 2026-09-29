# wisp documentation

## Guides

- [Using wisp](usage.md): the TUI, keys and commands, approvals, one-shot
  mode, sessions
- [Configuration](config.md): endpoint and model, every flag and variable,
  files, cost, OpenRouter
- [Tools](tools.md): the built-in tools, their limits, and how calls run
- [Permissions](permissions.md): what asks, what "always allow" covers,
  credentials, and project trust
- [Sub-agents](subagents.md): delegating to agents with their own model
  and tools
- [MCP servers](mcp.md): connecting servers and keeping their tools cheap
- [Tracing](tracing.md): browsing sessions as live timelines

## How it works

- [Architecture](architecture.md): the packages and one turn through them
- [Model provider](model-provider.md): streaming, reasoning, retries,
  model discovery
- [Context compaction](compaction.md): masking and summarizing to fit small
  windows
- [Sessions](session.md): the SQLite schema, persistence, and resume
