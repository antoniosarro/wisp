#!/usr/bin/env bash
# Screenshots or records the real wisp TUI, for visual QA and the docs.
# Starts a private headless sway, so nothing shows on or types into your
# desktop, and runs a kitty in it: the logo and mascot are real kitty
# graphics. Steps are typed into a shell in that kitty; the output is
# grabbed with grim, or recorded with wf-recorder when OUT ends in .gif.
#
# A local model is loaded before recording starts. Every run works in a
# throwaway clone of this repository, which wisp sees as ~/wisp, with empty
# state and config: shots don't depend on this machine, and nothing
# touches the project's sessions. It is deleted after.
#
# Usage: scripts/screenshot.sh OUT.png|OUT.gif [STEP...]
#   "text"     type text and press Enter: a prompt to wisp
#   /command   type a slash command and press Enter
#   run:CMD    type a shell command and press Enter, e.g. run:wisp
#   type:TEXT  type text without Enter, e.g. an approval answer: type:y
#   key:NAME   press a key, e.g. key:Escape, key:F1, key:ctrl+r, key:alt+Left
#   wait       wait until every prompt's turn ended or a tool call waits
#              for approval (read from the session's trace spans; gives
#              up after 10 minutes). In a GIF, a wait longer than
#              GIF_WAIT_MAX seconds (default 20) plays faster to fit.
#   sleep:N    wait N seconds
#   page:KEY   press a key in the trace page, with WISP_QA_TRACE, e.g.
#              page:ArrowDown
# Without a run: step, wisp is started before the shot. The shot gets 2 s
# before the first step and after the last one.
#
# Examples:
#   scripts/screenshot.sh docs/images/splash.png
#   scripts/screenshot.sh docs/images/chat.gif "Explain internal/core" wait
#   scripts/screenshot.sh docs/images/approval.gif "Run go vet ./..." wait sleep:2 type:y wait
#
# Environment: WISP_QA_MODEL (default fast-agent), WISP_QA_BASE_URL
# (default http://localhost:8091/v1), WISP_QA_KEY_FILE (the API key, for a
# hosted endpoint; never typed), WISP_QA_TRACE (set: the trace page opens
# in a dark Chromium beside kitty, following the session live; needs
# chromium and websocat), WISP_QA_CONFIG (a directory copied in as the
# config dir, e.g. with wisp/agents/*.md), WISP_QA_KEEP_DATA (a directory to copy
# wisp's data, with the session, into), WISP_QA_SIZE (default 1500x900),
# GIF_WIDTH (default 1100), GIF_FPS (default 10), GIF_WAIT_MAX (default 20).
set -euo pipefail

OUT="${1:?usage: scripts/screenshot.sh OUT.png|OUT.gif [STEP...]}"
shift
MODEL="${WISP_QA_MODEL:-fast-agent}"
BASE_URL="${WISP_QA_BASE_URL:-http://localhost:8091/v1}"
SIZE="${WISP_QA_SIZE:-1500x900}"
KEY=
[ -z "${WISP_QA_KEY_FILE:-}" ] || KEY="$(cat "$WISP_QA_KEY_FILE")"

cd "$(dirname "$0")/.."
OUT="$(realpath -m "$OUT")"
TMP="$(mktemp -d)"
mkdir "$TMP/bin"
go build -o "$TMP/bin/wisp" ./cmd/wisp

# Warm a local model up first: llama-swap loads it on the first request,
# which can take a minute, and the shots should show wisp, not the load.
case $BASE_URL in
http://localhost* | http://127.*)
	echo "Loading $MODEL..."
	curl -sf --max-time 600 "$BASE_URL/chat/completions" -H 'Content-Type: application/json' \
		-d "{\"model\": \"$MODEL\", \"messages\": [{\"role\": \"user\", \"content\": \"hi\"}], \"max_tokens\": 1}" >/dev/null ||
		{ echo "scripts/screenshot.sh: $MODEL at $BASE_URL didn't answer" >&2; exit 1; }
	;;
esac

git clone -q "file://$PWD" "$TMP/home/wisp"
mkdir -p "$TMP/config"
[ -z "${WISP_QA_CONFIG:-}" ] || cp -r "$WISP_QA_CONFIG/." "$TMP/config/"
SWAY_PID=
REC_PID=
TRACE_PID=
cleanup() {
	[ -z "$TRACE_PID" ] || kill "$TRACE_PID" 2>/dev/null || true
	[ -z "$REC_PID" ] || kill -INT "$REC_PID" 2>/dev/null || true
	[ -z "$SWAY_PID" ] || kill "$SWAY_PID" 2>/dev/null || true
	wait 2>/dev/null || true
	# wisp may still be closing its session files for a moment.
	rm -rf "$TMP" 2>/dev/null || { sleep 1; rm -rf "$TMP"; }
}
trap cleanup EXIT

# The private display: one headless output the size of the shot, where
# kitty, the only window, fills it.
cat >"$TMP/sway.cfg" <<EOF
output HEADLESS-1 resolution $SIZE
default_border none
exec sh -c 'echo \$WAYLAND_DISPLAY >$TMP/display'
EOF
WLR_BACKENDS=headless WLR_LIBINPUT_NO_DEVICES=1 sway -c "$TMP/sway.cfg" >"$TMP/sway.log" 2>&1 &
SWAY_PID=$!
for _ in $(seq 40); do [ -s "$TMP/display" ] && break; sleep 0.25; done
[ -s "$TMP/display" ] || { echo "scripts/screenshot.sh: headless sway didn't start:" >&2; cat "$TMP/sway.log" >&2; exit 1; }
export WAYLAND_DISPLAY="$(cat "$TMP/display")"

# HOME and the XDG dirs point into the clone's parent. The key is only in
# the environment. confirm_os_window_close=0: kitty would otherwise ask
# before closing.
kitty --directory "$TMP/home/wisp" -o confirm_os_window_close=0 \
	env HOME="$TMP/home" XDG_CONFIG_HOME="$TMP/config" XDG_STATE_HOME="$TMP/state" XDG_DATA_HOME="$TMP/data" \
	XDG_CACHE_HOME="$TMP/cache" PATH="$TMP/bin:$PATH" PS1='$ ' \
	WISP_BASE_URL="$BASE_URL" WISP_MODEL="$MODEL" WISP_API_KEY="$KEY" \
	GOMODCACHE="$(go env GOMODCACHE)" GOCACHE="$(go env GOCACHE)" \
	bash --norc --noprofile >/dev/null 2>&1 &
sleep 1.5
# Each wtype is a new virtual keyboard: a key sent before kitty has its
# keymap can be lost, so every wtype waits a moment first.
wt() { wtype -s 150 "$@"; }
wt "clear" -k Return
case " $* " in
*" run:"*) ;;
*) # started before the recording; its session database shows it did
	wt "wisp" -k Return
	for _ in $(seq 40); do [ -e "$TMP/data/wisp/session.db" ] && break; sleep 0.25; done
	[ -e "$TMP/data/wisp/session.db" ] || { echo "scripts/screenshot.sh: wisp didn't start" >&2; exit 1; }
	sleep 1.5 ;;
esac

# The trace page beside kitty, served from the session database wisp just
# created. Sway tiles the two side by side; the keyboard goes back to kitty.
sock() { ls "$XDG_RUNTIME_DIR"/sway-ipc.*."$SWAY_PID".sock; }
if [ -n "${WISP_QA_TRACE:-}" ]; then
	XDG_DATA_HOME="$TMP/data" XDG_STATE_HOME="$TMP/state" "$TMP/bin/wisp" --trace-only --trace-addr 127.0.0.1:0 >"$TMP/trace-url" 2>&1 &
	TRACE_PID=$!
	for _ in $(seq 40); do grep -q '^wisp trace: ' "$TMP/trace-url" && break; sleep 0.25; done
	url="$(sed -n 's/^wisp trace: \([^ ]*\).*/\1/p' "$TMP/trace-url")"
	[ -n "$url" ] || { echo "scripts/screenshot.sh: wisp --trace-only gave no URL:" >&2; cat "$TMP/trace-url" >&2; exit 1; }
	chromium --ozone-platform=wayland --user-data-dir="$TMP/chromium" --no-first-run --disable-gpu \
		--blink-settings=preferredColorScheme=0 --remote-debugging-port=0 --app="$url" >/dev/null 2>&1 &
	sleep 4
	swaymsg -s "$(sock)" focus left >/dev/null
fi

if [[ $OUT == *.gif ]]; then
	# Lossless RGB: the usual YUV video squeezes colors into TV range,
	# which comes back washed out (#1e1e2e as #292837). -D takes every
	# frame: on a still screen, wf-recorder would wait forever for one
	# before stopping.
	wf-recorder -D -r 20 -c libx264rgb -x bgr0 -p crf=0 -p preset=ultrafast -y -f "$TMP/rec.mkv" >/dev/null 2>&1 &
	REC_PID=$!
	REC_START=$(date +%s.%N)
fi
# idle succeeds once every prompt sent has a finished turn, or a tool call
# waits for approval: top-level turn spans have no parent, and a pending
# approval's span has no end yet.
DB="$TMP/data/wisp/session.db"
prompts=0
waits=() # START:END of each wait, in seconds into the recording
idle() {
	sqlite3 "$DB" "SELECT (SELECT count(*) FROM spans WHERE kind = 'turn' AND parent_id = '' AND end_ns > 0) >= $prompts
		OR EXISTS (SELECT 1 FROM spans WHERE kind = 'approval' AND end_ns = 0)" 2>/dev/null | grep -qx 1
}

# key presses a named key (an XKB name: Return, Next, Left), or a chord
# with ctrl+ or alt+, e.g. ctrl+r, alt+Left. Typed text goes at a
# readable pace.
key() {
	local mods=() k=$1
	while [[ $k == ctrl+* || $k == alt+* ]]; do
		mods+=("${k%%+*}")
		k=${k#*+}
	done
	local args=()
	for m in "${mods[@]}"; do args+=(-M "$m"); done
	if [ "${#k}" -eq 1 ]; then args+=("$k"); else args+=(-k "$k"); fi
	for m in "${mods[@]}"; do args+=(-m "$m"); done
	wt "${args[@]}"
}
typed() { wt -d 25 "$1"; }

# page presses a key in the trace page through Chromium's DevTools
# protocol: Chromium ignores wtype's virtual keyboard.
page() {
	local port ws t
	port="$(head -1 "$TMP/chromium/DevToolsActivePort")"
	ws="$(curl -s "http://127.0.0.1:$port/json/list" | jq -r '[.[] | select(.type == "page")][0].webSocketDebuggerUrl')"
	for t in keyDown keyUp; do
		printf '{"id":1,"method":"Input.dispatchKeyEvent","params":{"type":"%s","key":"%s","code":"%s"}}\n' "$t" "$1" "$1" |
			websocat -n1 "$ws" >/dev/null
	done
}

sleep 2
for step in "$@"; do
	case $step in
		wait)
			start=$(date +%s.%N)
			sleep 1
			for _ in $(seq 600); do idle && break; sleep 1; done
			[ -z "$REC_PID" ] || waits+=("$(awk -v s="$start" -v e="$(date +%s.%N)" -v r="$REC_START" 'BEGIN { print s - r ":" e - r }')")
			;;
		sleep:*) sleep "${step#sleep:}" ;;
		key:*) key "${step#key:}" ;;
		type:*) typed "${step#type:}" ;;
		run:*) typed "${step#run:}"; wt -k Return ;;
		page:*) page "${step#page:}" ;;
		/*) typed "$step"; sleep 0.3; wt -k Return ;;
		*) typed "$step"; wt -k Return; prompts=$((prompts + 1)) ;;
	esac
done
sleep 2

mkdir -p "$(dirname "$OUT")"
if [[ $OUT == *.gif ]]; then
	kill -INT "$REC_PID"; wait "$REC_PID" || true; REC_PID=
	mkdir "$TMP/frames"
	ffmpeg -loglevel error -i "$TMP/rec.mkv" -vf "fps=${GIF_FPS:-10}" "$TMP/frames/%05d.png"
	# Waiting for the model is mostly a spinner: a long wait keeps an even
	# spread of its frames, so it plays faster but nothing in it is lost.
	for w in "${waits[@]}"; do
		awk -v w="$w" -v fps="${GIF_FPS:-10}" -v max="${GIF_WAIT_MAX:-20}" -v dir="$TMP/frames" 'BEGIN {
			split(w, t, ":"); a = int(t[1] * fps) + 1; b = int(t[2] * fps); n = b - a + 1; m = int(max * fps)
			if (n <= m) exit
			for (k = 0; k < m; k++) keep[a + int(k * n / m)] = 1
			for (i = a; i <= b; i++) if (!(i in keep)) printf "%s/%05d.png\n", dir, i
		}' | xargs -r rm -f
	done
	gifski --quiet --fps "${GIF_FPS:-10}" --width "${GIF_WIDTH:-1100}" -o "$OUT" "$TMP"/frames/*.png
else
	grim "$OUT"
fi
echo "wrote $OUT"
if [ -n "${WISP_QA_KEEP_DATA:-}" ]; then
	mkdir -p "$WISP_QA_KEEP_DATA"
	cp -r "$TMP/data/." "$WISP_QA_KEEP_DATA"
fi
