# Model provider

How wisp talks to a model: the provider-agnostic contract in
`internal/model`, and its one implementation, `internal/model/openaicompat`.

## The contract

The core loop depends only on `internal/model`; nothing there knows a wire
format. The full types, with field-level comments, are in `model.go`,
`event.go`, and `info.go`.

```go
type Provider interface {
    Stream(ctx context.Context, req Request) (<-chan Event, error)
}
```

- **`Request`** carries the conversation (`Messages`), the tool schemas, and
  per-request controls: `ToolChoice`, `MaxTokens`, `NoReasoning`, a
  `ReasoningTokens` budget, and an `Effort` level.
- **`Event`** is one unit of the streamed response. Its `Kind` is one of
  `TextDelta`, `ReasoningDelta`, `ToolCall`, `Done` (with `Usage` and
  `Truncated`), `Error`, or `Reclassify`, which says the text streamed so far
  was really reasoning.
- **Errors.** `Stream` returns an error only when the request never started.
  After that, a failure arrives as an `EventError`, and the channel closes
  after `Done` or `Error`. An error for a request that no longer fits the
  context window wraps `model.ErrContextOverflow`.

The TUI renders deltas as they arrive, reasoning included (dimmed or
collapsed, apart from the answer).

## The OpenAI-compatible client

`openaicompat` covers local servers (llama.cpp, llama-swap, vLLM, SGLang,
Ollama, LM Studio) and hosted OpenAI-compatible APIs such as OpenRouter.

| File | Concern |
| --- | --- |
| `client.go` | `Config` and the `Client` |
| `request.go` | the chat request and its retries |
| `http.go` | shared HTTP plumbing, retry policy, error classification |
| `openrouter.go` | OpenRouter routing, attribution, and cost |
| `stream.go`, `sse.go`, `think.go` | reading the streamed response |
| `catalog.go`, `listing.go`, `native.go` | model discovery |

### Requests

- **Images.** Tool results carry only text, so images returned by a run of
  tool calls follow it in one user message, as data URLs.
- **Reasoning.** `NoReasoning` becomes `reasoning.enabled=false` on
  OpenRouter and `chat_template_kwargs` (`enable_thinking` and `thinking`
  set to false) elsewhere. A `ReasoningTokens` budget is only sent to
  OpenRouter; other servers leave reasoning to the model. An `Effort`
  level becomes `reasoning.effort` on OpenRouter and `reasoning_effort`
  elsewhere. A local server also gets it in `chat_template_kwargs`, as
  `reasoning_effort`, or for `none` as the variables that turn thinking
  off. `NoReasoning` wins over an `Effort`.
- **Retries.** A request that fails before streaming starts (dropped or
  refused connection, 429, 5xx) is retried up to 3 times, after 1 s, 2 s,
  and 4 s. A longer `Retry-After` (in seconds) is honored, up to 60 s.
  Other 4xx responses fail at once, since they would fail the same way
  again.

### Responses

- **Reasoning.** Visible chain-of-thought arrives as a `reasoning_content`
  or `reasoning` field on the delta, or as inline `<think>...</think>` in
  the text. Both become `Event.Reasoning`. Only a tag opening the response
  counts, so an answer that mentions the tags stays text.
- **`</think>` without `<think>`.** The chat template opened the block in
  the prompt. A bare closing tag at the start or end of a line ends the
  reasoning, and the text before it is reclassified. Inside a line, it is
  text that mentions the tag.
- **Tool calls.** Fragments are joined by `index`. Calls sent whole without
  `index` are kept apart by their id. A missing or duplicate id is replaced
  with a fresh one, since results are matched to calls by id.
- **Truncation.** A response cut off at the output limit
  (`finish_reason=length`) keeps its text and drops its tool calls, whose
  arguments may be incomplete. The loop continues with a reminder to act
  rather than restart.
- **Failures.** These all end the response with an error:
  - an error chunk (`{"error": ...}`) inside the stream
  - any other `finish_reason`, e.g. `content_filter`
  - a stream that ends without `[DONE]`

  A connection that drops before the first event is retried once. A drop
  after the answer started is not, because the caller already has part of
  it.
- **Limits.** A stream that sends nothing for 10 minutes is cut; a local
  server may take minutes on a long prompt before its first token. SSE lines
  may be up to 16 MiB, enough for a tool call that writes a big file.

### OpenRouter

Detected from the base URL's host.

- **Privacy.** Requests go only to providers that neither train on nor
  retain prompts (`data_collection: deny`, `zdr: true`).
- **Routing**, picked in this order:
  1. `Config.Provider` pins every request to one provider, keeping its
     prompt cache warm.
  2. `Config.Cheapest` sends requests to the two cheapest providers of the
     model that support tool calls, in that order. They are ranked by the
     cost of 10 prompt tokens per completion token, from
     `/endpoints/zdr`. If that lookup fails, it falls back to price sort.
  3. Otherwise, OpenRouter's price sort chooses.

  The choice is recorded on the request's trace span (`wisp.routing`).
- **Cost.** Each request asks for its billed cost, reported in
  `Usage.Cost` with the upstream provider that served it.
- **Attribution.** With `AppURL` set, requests carry wisp's app attribution
  headers. These go to OpenRouter only. [config.md](config.md#openrouter)
  covers the flags.

## Model discovery

The client implements `model.Catalog`:

```go
type Catalog interface {
    Models(ctx context.Context) ([]Info, error)          // the listing, with what it reports
    Describe(ctx context.Context, id string) (Info, error) // everything known about one model
    Model() string
    SetModel(id string)
}
```

`Info` holds the server's per-request context window and the model's trained
maximum, the output cap, tool, vision, and reasoning support (unknown,
supported, or unsupported), load state, whether it is an embedding model,
whether the server is local (loopback, private, Tailscale, or LAN host), and
its price per 1M tokens, and the reasoning effort levels it takes. Zero
values mean the server didn't say.

- **Listing.** `/models` carries only ids in the OpenAI spec, but servers
  add fields of their own:
  - vLLM and SGLang: `max_model_len`
  - llama.cpp: `meta.n_ctx_train` and a `models[].capabilities` list
  - llama-swap: `meta.llamaswap.context_length`
  - OpenRouter: `context_length`, `architecture` modalities,
    `supported_parameters`, `reasoning`, `top_provider.max_completion_tokens`,
    and `pricing`

  OpenRouter prices are in dollars per token and are converted to per
  million. Its `-1`, used for variable-price routers, means unknown. The
  listing is reused for 30 s.
- **Native endpoints.** When the listing lacks context sizes, the client
  probes local servers' own endpoints under the server root (the base URL
  without `/v1`):
  - LM Studio: `/api/v0/models`
  - Ollama: `/api/ps`
  - llama.cpp: `/props`, only for a server with one model

  `Describe` also asks llama.cpp `/props`, Ollama `/api/show`, and, on
  llama-swap, the running model's `/upstream/ID/props` (never a model
  that isn't running: asking would load it). Probes
  run concurrently, and a failed probe is skipped. A GET answered with 404,
  405, or 501 is never asked again, because every server 404s the others'
  endpoints.
- **Reasoning effort levels.** llama.cpp's `/props` reports whether the
  chat template takes `reasoning_effort` (then `low`, `medium`, `high`), and
  a template reading `enable_thinking` can turn reasoning off (`none`).
  OpenRouter's listing gives each reasoning model's `reasoning` details:
  its `supported_efforts`, and `none` unless reasoning is `mandatory`.
  Levels wisp doesn't know are dropped.
- **Context window.** `ContextWindow` is only what the server applies per
  request. For Ollama, that is the loaded context or the model's `num_ctx`,
  never the trained maximum, which Ollama would silently truncate to its
  default.
