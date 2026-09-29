#!/usr/bin/env bash
# Prints a Conventional Commits message for the staged changes, written by
# a local model behind llama-swap (just commit uses it). Set COMMIT_MODEL
# and COMMIT_MODEL_URL to use another model or endpoint.
set -euo pipefail

url=${COMMIT_MODEL_URL:-http://localhost:8091/v1}
model=${COMMIT_MODEL:-coder-35b}

if git diff --cached --quiet; then
	echo "commit-msg: nothing staged; git add what the commit should contain" >&2
	exit 1
fi

system='You write git commit messages following Conventional Commits 1.0.
Format:
<type>(<scope>): <subject>

<body>

Rules:
- type is one of: feat, fix, refactor, perf, test, docs, build, ci, chore, style.
- scope is the main package or area touched, e.g. core, tui, cli, tool, model, session; omit "(<scope>)" when the change spans the whole project.
- subject: imperative mood, lower case, no final period, at most 72 characters.
- body: why the change was made and what it does, wrapped at 72 columns; omit it for a trivial change.
- Add "BREAKING CHANGE: <what>" as the last paragraph only when behavior users rely on changes.
Reply with the commit message only: no code fences, no preamble.'

# The stat line keeps the whole change in view when the diff is cut.
changes="Recent commits, for the scope names in use:
$(git log --oneline -10 2>/dev/null || true)

Staged changes:
$(git diff --cached --stat)

$(git diff --cached --unified=2 | head -c 60000)"

# Thinking off (as wisp does for a summary): a reasoning model would
# otherwise spend the token budget, and minutes, before the first word.
request=$(jq -n --arg model "$model" --arg system "$system" --arg changes "$changes" '{
	model: $model, temperature: 0.2, max_tokens: 1024,
	chat_template_kwargs: {enable_thinking: false, thinking: false},
	messages: [{role: "system", content: $system}, {role: "user", content: $changes}]
}')

reply=$(curl -sS --fail-with-body --max-time 300 "$url/chat/completions" \
	-H 'Content-Type: application/json' -d "$request") || {
	echo "commit-msg: $model at $url failed: $reply" >&2
	exit 1
}

# Reasoning models may inline their thinking; drop it, any code fences,
# and leading blank lines.
printf '%s\n' "$reply" | jq -r '.choices[0].message.content // empty' |
	sed '/<think>/,/<\/think>/d; /^```/d' | sed '/./,$!d'
