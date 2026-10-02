# Tools

What the model can do, and how its calls run: the contract and dispatcher
in `internal/tool`, and the built-in tools in `internal/tool/builtin`.

## The contract

```go
type Tool interface {
    Schema() model.ToolSchema // name, description, and JSON schema for the model
    Risky() bool              // whether calls go through the permission layer
    Run(ctx context.Context, args json.RawMessage) (Result, error)
}

// Optional: a tool whose risk depends on the call.
type CallRisk interface {
    RiskyCall(args json.RawMessage) bool
}
```

- **Result.** `Result` carries the text for the model, any images, and
  `IsError` for a failure the model should read, such as a command's
  non-zero exit. A Go error from `Run` means the call couldn't be carried
  out at all: bad arguments, a missing file, or a cancelled turn.
- **Risk per call.** `CallRisk` lets one tool be safe for most calls and
  risky for some. `read` and `grep` ask only for credential paths; a proxy
  to MCP tools asks only for tools that aren't read-only.
- **Same contract for all.** Built-in tools, MCP tools (through
  `mcp_call`), and the `agent` tool all implement it, and the dispatcher
  doesn't tell them apart.

## Built-in tools

| Tool | Purpose | Asks? |
| --- | --- | --- |
| `read` | Read a file, or a line range of it, with numbered lines | Credentials only |
| `ls` | List one directory | No |
| `glob` | Find files by pattern (`**` for recursion) | No |
| `grep` | Search file contents with a regular expression | Credentials only |
| `todo` | Write the model's task list | No |
| `write` | Create or overwrite a file | Yes |
| `edit` | Replace an exact string in a file | Yes |
| `multi_edit` | Several replacements in one file, all or nothing | Yes |
| `bash` | Run a non-interactive command | Yes |
| `fetch` | Download an http(s) URL as text | Yes |
| `agent` | Delegate a task to a configured sub-agent ([subagents.md](subagents.md)) | No; its calls ask |
| `tool_search` | Find MCP tools and load their definitions into the conversation ([mcp.md](mcp.md)) | No |
| `mcp_call` | Call an MCP tool found with `tool_search` | Unless the MCP tool is read-only |

`agent` exists only when an agent file is configured, and `tool_search` and
`mcp_call` only when MCP servers are. "Asks" means the call goes through the
permission layer ([permissions.md](permissions.md)) unless the user allowed
matching calls for the session.

### Reading and searching

- **`read`.**
  - Output stops at 2000 lines or 50 KB, and lines over 2000 bytes are cut.
    A note gives the offset to continue from.
  - Images (PNG, JPEG, GIF, WebP, up to 10 MB) are attached for vision
    models and only reported to others.
  - Other binary files are reported, not dumped.
  - FIFOs, devices, and directories are refused, since opening a FIFO would
    block the call.
- **`ls`.** Subdirectories come first, ending in `/`; then files with their
  sizes, and symlinks ending in `@`. At most 200 entries.
- **`glob` and `grep`.**
  - Results are paths usable from the working directory. At most 200 are
    shown, and a search stops counting at 1000, saying so, rather than
    reading the whole tree.
  - Walks skip `.git`, `node_modules`, `vendor`, and `.direnv`.
  - `grep` uses Go's RE2 regular expressions (`(?i)` for case-insensitive)
    and shows up to 300 bytes around each match. Its `glob` argument
    filters which files it searches, and `files_only` lists just the files
    that match.
  - `grep` skips binary files, FIFOs, and devices, and reports files it
    couldn't read.
- **`todo`.** It is stateless: each call sends the whole list, so the plan
  lives in the conversation. The result only acknowledges it, since echoing
  the list would cost its tokens twice. While the latest list has
  unfinished tasks, `TodoReminder` asks the model, before it ends its turn,
  to finish them or mark them done.

### Changing files

- **Atomic writes.** `write`, `edit`, and `multi_edit` write to a temporary
  file and rename it into place, so a crash leaves the old or the new
  content, never half of it.
- **Modes and symlinks.** An existing file keeps its mode. A symlink is
  followed and its target replaced.
- **`write`** creates missing parent directories. Content is required: an
  empty file takes an explicit `""`.
- **`edit` and `multi_edit`** match exactly, not fuzzily, so an edit never
  lands somewhere the model didn't mean.
  - `old_string` must match once unless `replace_all` is set, and deleting
    text takes an explicit empty `new_string`.
  - In a CRLF file, an edit written with LF line endings is converted.
  - When `old_string` isn't found, the error says why: line-number prefixes
    copied from `read`, whitespace that differs, or where the first
    distinctive line of it is in the file.
  - `multi_edit` applies its edits in order, each seeing the previous one's
    result. If any fails, nothing is written.

### Running commands: `bash`

- **One fresh shell per call.** Each call runs `bash -c` in the working
  directory. The default timeout is 120 s, and a call may set up to 600 s.
- **Nothing waits for input.** Pagers are set to `cat`, and git doesn't
  prompt for credentials.
- **Output** keeps stdout, then stderr, then the exit code. Over 30 KB, it
  keeps its head and its tail (where errors usually are), and the full text
  is saved to a temporary file the note names, pruned after 24 hours. Each
  stream is captured up to 8 MB.
- **Background processes.** When the command returns, any it left running
  are killed. Their output so far is kept, and a note says they were
  stopped. Start long-lived servers outside wisp. On a timeout or a
  cancelled turn, the whole process group is killed (Linux).

### The web: `fetch`

- **Output.** HTML is converted to text, keeping headings and list items.
  Text, JSON, and XML are returned as they are, and anything else is
  refused.
- **Paging.** A call returns 20 KB by default, up to 100 KB, and `offset`
  continues a long page. Downloads stop at 5 MB, and a call at 30 s.
- **Error pages** (4xx, 5xx) are an error result showing the start of the
  page.
- **Redirects** are followed within the host only. A redirect to another
  host is not followed: the result names the target and tells the model to
  fetch that URL. That is a new call, approved for its own host, so an
  allowed host can't redirect to a local service or a cloud metadata
  address.
- **Local addresses are refused.** `fetch` won't connect to loopback,
  private, link-local (including the cloud metadata address
  `169.254.169.254`) or shared (`100.64.0.0/10`) addresses. The check is
  made on the address being connected to, after DNS, so a public name that
  resolves to a local address is refused too.
- **Your own network.** To reach services on your LAN or homelab, list
  their addresses with `--fetch-allow` or `$WISP_FETCH_ALLOW`, as IPs or
  CIDR prefixes: `--fetch-allow 192.168.1.0/24,10.0.0.5`. Names aren't
  accepted; list the address they resolve to. Each host still asks before
  the first fetch.
- **Proxies.** `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` are followed, and
  the proxy itself may be a local address. The proxy resolves the host
  itself, so wisp resolves it first and refuses a local address. A host
  that doesn't resolve locally is left to the proxy, since on networks
  that reach the web only through one, outside names often don't.

## Credentials and isolation

- **Credentials.** `read` and `grep` ask before reading a credential path,
  such as a file under `~/.ssh` or a `.env`. Wider searches skip them: a
  walk skips credential directories, and `grep` skips credential files it
  meets, saying how many. `glob` still lists such files by name. The rules
  and the full lists are in [permissions.md](permissions.md#credentials).
- **Environment.** Processes that tools start (bash commands, stdio MCP
  servers) get wisp's environment without `WISP_API_KEY`.
- **Terminal output.** Tool output shown in the terminal is stripped of
  escape sequences and control characters (`internal/termsafe`).

## Dispatch

- **Order.** Results come back in the order the model requested the calls,
  not the order they finish in, so the context stays deterministic. A
  callback reports each call as it completes, so the UI can show progress.
- **Concurrency.** Read-only calls run concurrently. A risky call is a
  barrier: it waits for the calls before it and holds back those after, so
  writes and commands apply in the order the model asked for them.
- **Isolation.** Each call gets its own `context.Context`, so a stuck call
  doesn't block the others, and Esc cancels them all.
- **Time limits.** Tools bound their own time: `bash` has its timeout,
  `fetch` 30 s, an MCP call 10 minutes. A sub-agent may rightly run longer.
- **Failures.** An unknown tool, or a tool that panics, fails its call only.
  A panic doesn't take down the process and the terminal's state with it.
- **Tracing.** Each call is traced as a span with its arguments, result,
  and status, named after OpenTelemetry's GenAI conventions
  (`internal/span`).
- **Repeated calls.** A read-only call identical to one already run in the
  same turn (same tool, same arguments as JSON), with no risky call since,
  isn't run again. Its result is a short note that the earlier result still
  holds, marked as an error. A risky call (a write, `bash`, a non-read-only
  MCP tool) resets this, since it may change what reads return, and so does
  each new user turn. Small models otherwise loop on the same call (13
  identical `accounts_list` calls in one traced turn), and each repeat puts
  the same output in context again.

## Git awareness

Not a separate tool: `bash` runs `git` directly, and the system prompt
names the current branch.
