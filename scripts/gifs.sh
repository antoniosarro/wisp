#!/usr/bin/env bash
# Re-creates the docs' images, docs/images/*, after a change to how wisp
# looks. Each is recorded by scripts/screenshot.sh in a private headless
# display, so nothing shows on or types into your desktop. They run on
# OpenRouter, so they show prices, routing, and effort levels; the model's
# answers vary from run to run.
#
# Usage: scripts/gifs.sh [NAME...]   (default: all of them; see list)
# Environment: WISP_QA_KEY_FILE, the file holding the OpenRouter key
# (never typed); WISP_QA_MODEL (default deepseek/deepseek-v4-flash-0731).
set -euo pipefail

cd "$(dirname "$0")/.."
: "${WISP_QA_KEY_FILE:?set WISP_QA_KEY_FILE to the file holding the OpenRouter key}"
export WISP_QA_BASE_URL=https://openrouter.ai/api/v1
export WISP_QA_MODEL="${WISP_QA_MODEL:-deepseek/deepseek-v4-flash-0731}"

# Every image, in the order a full run makes them.
list=(splash tour chat effort tools todo approval agents cost trace-live trace)

shot() {
	local out=$1
	shift
	echo "Recording $out..."
	scripts/screenshot.sh "docs/images/$out" "$@"
}

# trace records a short session, serves it with wisp --trace-only, and
# screenshots the page in headless Chromium, in each color scheme.
trace() {
	local tmp
	tmp="$(mktemp -d)"
	trap 'kill $(jobs -p) 2>/dev/null; rm -rf "$tmp"' RETURN
	echo "Recording a session for trace.png and trace-dark.png..."
	WISP_QA_KEEP_DATA="$tmp/data" scripts/screenshot.sh "$tmp/session.png" \
		"Read internal/core/budget.go and explain it in two sentences." wait
	go build -o "$tmp/wisp" ./cmd/wisp
	XDG_DATA_HOME="$tmp/data" XDG_STATE_HOME="$tmp/state" "$tmp/wisp" --trace-only --trace-addr 127.0.0.1:0 >"$tmp/url" &
	local url=
	for _ in $(seq 40); do
		url="$(sed -n 's/^wisp trace: \([^ ]*\).*/\1/p' "$tmp/url")"
		[ -n "$url" ] && break
		sleep 0.25
	done
	[ -n "$url" ] || { echo "scripts/gifs.sh: wisp --trace-only gave no URL" >&2; return 1; }
	# preferredColorScheme: 0 is dark, 1 light.
	local out scheme
	for out in trace.png:1 trace-dark.png:0; do
		scheme=${out#*:}
		out=${out%:*}
		nix shell nixpkgs#chromium -c chromium --headless --disable-gpu --hide-scrollbars \
			--user-data-dir="$tmp/chromium" --window-size=1500,900 --virtual-time-budget=10000 \
			--blink-settings=preferredColorScheme="$scheme" --screenshot="docs/images/$out" "$url" 2>/dev/null
		echo "wrote docs/images/$out"
	done
}

record() {
	case $1 in
	# The splash screen.
	splash) shot splash.png ;;
	# The command popup, help, and the model and effort pickers.
	tour) shot tour.gif sleep:3 \
		type:/ sleep:1.5 key:Down sleep:0.5 key:Down sleep:0.5 key:Down sleep:1 type:mo sleep:1.5 \
		key:Escape key:BackSpace key:BackSpace key:BackSpace sleep:0.5 \
		/help sleep:3.5 key:Next sleep:2 key:Escape sleep:0.8 \
		/model sleep:3 type:deepseek sleep:2 key:Down sleep:0.6 key:Down sleep:1.5 key:Escape sleep:0.8 \
		/effort sleep:2.5 key:Escape sleep:1 ;;
	# wisp answering a question about its own code.
	chat) shot chat.gif "Read the files in internal/core and explain what it does, in a few sentences. Don't run commands." wait sleep:4 ;;
	# Reasoning effort: high, the reasoning opened with Ctrl+R, then none.
	effort) shot effort.gif \
		/effort sleep:1.5 type:high sleep:1 key:Return sleep:2 \
		"A bat and a ball cost \$1.10. The bat costs \$1.00 more than the ball. What does the ball cost? One sentence." wait \
		sleep:2 key:ctrl+r sleep:3 key:ctrl+r sleep:1 \
		"/effort none" sleep:1.5 "Same question again: answer in one sentence." wait sleep:3 ;;
	# Tool calls: searching and reading, then a call's output expanded.
	tools) shot tools.gif \
		"Find where wisp decides that a path holds credentials, and show me that function. Be brief." wait \
		sleep:2 key:alt+Left sleep:1 key:alt+Left sleep:1 key:ctrl+o sleep:3 key:ctrl+o sleep:1.5 ;;
	# A plan in the task panel, worked through step by step.
	todo) shot todo.gif \
		"Use the todo tool to plan a three-step review of internal/permission, then do each step by reading the files. End with three bullets." \
		wait sleep:2 /todo sleep:2 /todo sleep:1 ;;
	# A command needs approval: y allows it once.
	approval) shot approval.gif "Run go vet ./internal/model/... and tell me if it passes." wait sleep:3 type:y wait sleep:4 ;;
	# Two sub-agents at once, the sub-agent panel, and one's own chat:
	# Alt+Left steps back from the answer past the reasoning to the
	# second sub-agent's box.
	# The explorer agent in scripts/qa/config: the agent tool only exists
	# when an agent is configured.
	agents) WISP_QA_CONFIG=scripts/qa/config shot agents.gif run:"wisp --max-agents 2" sleep:2 \
		"Start two explorer sub-agents at the same time: one explains internal/mcp, the other internal/session. Then combine their answers in four sentences." \
		wait sleep:2 /agents sleep:3 \
		key:alt+Left sleep:0.6 key:alt+Left sleep:0.6 key:alt+Left sleep:0.6 key:ctrl+o sleep:4 key:ctrl+b sleep:1.5 ;;
	# OpenRouter's cheapest providers, /context, /debug with the provider
	# that served the request, then sessions and one-shot mode.
	cost) shot cost.gif run:"wisp --cheapest" sleep:2 \
		"What does internal/core/budget.go do? Two sentences." wait sleep:2 \
		/context sleep:3.5 /debug sleep:4 /debug sleep:1 \
		key:ctrl+c key:ctrl+c sleep:1 \
		run:"wisp --sessions" sleep:3 run:"wisp 'In one sentence: what is wisp?'" sleep:15 ;;
	# The trace page beside wisp, filling in live as it works, then its
	# spans stepped through with the arrow keys.
	trace-live)
		echo "Recording trace-live.gif..."
		WISP_QA_TRACE=1 WISP_QA_SIZE=2400x1100 GIF_WIDTH=1400 nix shell nixpkgs#chromium nixpkgs#websocat -c \
			scripts/screenshot.sh docs/images/trace-live.gif \
			"Read internal/core/budget.go and internal/core/mask.go, then explain in three sentences how they keep requests in the window." \
			wait sleep:2 \
			page:ArrowDown sleep:1.5 page:ArrowDown sleep:1.5 page:ArrowDown sleep:1.5 page:ArrowDown sleep:1.5 page:ArrowDown sleep:3 ;;
	# The trace page, light and dark, showing a session recorded for it.
	trace) trace ;;
	*) echo "scripts/gifs.sh: no image named $1; there are: ${list[*]}" >&2; return 1 ;;
	esac
}

[ "$#" -gt 0 ] || set -- "${list[@]}"
for name in "$@"; do
	record "$name"
done
