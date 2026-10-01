#!/usr/bin/env bash
# Screenshots or records the real wisp TUI, for visual QA and the docs.
# Opens a kitty window on the current Hyprland session (headless capture
# fails silently in sandboxes), runs the steps in it, and grabs the window
# with grim, or records it with wf-recorder when OUT ends in .gif.
#
# The model is loaded before the window opens. Every run works in a throwaway clone of this repository, which wisp sees
# as ~/wisp, with empty state and config: shots don't depend on this
# machine, and nothing touches the project's sessions. It is deleted after.
#
# Usage: scripts/screenshot.sh OUT.png|OUT.gif [STEP...]
#   "text"     type text and press Enter
#   type:TEXT  type text without Enter, e.g. an approval answer: type:y
#   key:NAME   press a key, e.g. key:Escape, key:F1
#   wait       wait until the turn ends or asks for approval (read from
#              the session's trace spans; gives up after 10 minutes)
#   sleep:N    wait N seconds
# The window gets 2 s to show the splash before the first step, and 2 s
# after the last one.
#
# Examples:
#   scripts/screenshot.sh docs/images/splash.png
#   scripts/screenshot.sh docs/images/chat.gif "Explain internal/core" wait
#   scripts/screenshot.sh docs/images/approval.gif "Run go vet ./..." wait sleep:2 type:y wait
#
# Environment: WISP_QA_MODEL (default fast-agent), WISP_QA_BASE_URL
# (default http://localhost:8091/v1), WISP_QA_SIZE (default 1500x900),
# GIF_WIDTH (default 1100), GIF_FPS (default 10).
set -euo pipefail

OUT="${1:?usage: scripts/screenshot.sh OUT.png|OUT.gif [STEP...]}"
shift
MODEL="${WISP_QA_MODEL:-fast-agent}"
BASE_URL="${WISP_QA_BASE_URL:-http://localhost:8091/v1}"
SIZE="${WISP_QA_SIZE:-1500x900}"

cd "$(dirname "$0")/.."
OUT="$(realpath -m "$OUT")"
go build -o bin/wisp ./cmd/wisp
WISP_BINARY="$PWD/bin/wisp"

# Warm the model up first: llama-swap loads it on the first request, which
# can take a minute, and the shots should show wisp, not the load.
echo "Loading $MODEL..."
curl -sf --max-time 600 "$BASE_URL/chat/completions" -H 'Content-Type: application/json' \
	-d "{\"model\": \"$MODEL\", \"messages\": [{\"role\": \"user\", \"content\": \"hi\"}], \"max_tokens\": 1}" >/dev/null ||
	{ echo "scripts/screenshot.sh: $MODEL at $BASE_URL didn't answer" >&2; exit 1; }

TMP="$(mktemp -d)"
git clone -q "file://$PWD" "$TMP/home/wisp"
TITLE="wisp-qa-$$"
REC_PID=
KITTY_PID=
cleanup() {
	[ -z "$REC_PID" ] || kill -INT "$REC_PID" 2>/dev/null || true
	[ -z "$KITTY_PID" ] || kill "$KITTY_PID" 2>/dev/null || true
	wait 2>/dev/null || true
	# wisp may still be closing its session files for a moment.
	rm -rf "$TMP" 2>/dev/null || { sleep 1; rm -rf "$TMP"; }
}
trap cleanup EXIT

# HOME and the XDG state and data dirs point into the clone's parent; WISP_API_KEY is
# for remote providers and stays out of the local endpoint's requests.
# confirm_os_window_close=0: kitty would otherwise ask before closing.
kitty --title "$TITLE" --directory "$TMP/home/wisp" -o remember_window_size=no -o confirm_os_window_close=0 \
	env -u WISP_API_KEY HOME="$TMP/home" XDG_STATE_HOME="$TMP/state" XDG_DATA_HOME="$TMP/data" \
	"$WISP_BINARY" --model "$MODEL" --base-url "$BASE_URL" >/dev/null 2>&1 &
KITTY_PID=$!
sleep 1.5
focus() { hyprctl dispatch focuswindow "title:$TITLE" >/dev/null; sleep 0.2; }
hyprctl dispatch setfloating "title:$TITLE" >/dev/null
hyprctl dispatch resizewindowpixel "exact ${SIZE/x/ },title:$TITLE" >/dev/null
focus
hyprctl dispatch centerwindow >/dev/null
sleep 0.5

WIN=$(hyprctl -j clients | jq -r --arg t "$TITLE" '.[] | select(.title==$t) | "\(.at[0]),\(.at[1]) \(.size[0])x\(.size[1])"')
[ -n "$WIN" ] || { echo "scripts/screenshot.sh: could not find window titled $TITLE" >&2; exit 1; }

if [[ $OUT == *.gif ]]; then
	wf-recorder -g "$WIN" -r 20 -y -f "$TMP/rec.mp4" >/dev/null 2>&1 &
	REC_PID=$!
fi
# idle succeeds once every prompt sent has a finished turn, or a tool call
# waits for approval: top-level turn spans have no parent, and a pending
# approval's span has no end yet.
DB="$TMP/data/wisp/session.db"
prompts=0
idle() {
	sqlite3 "$DB" "SELECT (SELECT count(*) FROM spans WHERE kind = 'turn' AND parent_id = '' AND end_ns > 0) >= $prompts
		OR EXISTS (SELECT 1 FROM spans WHERE kind = 'approval' AND end_ns = 0)" 2>/dev/null | grep -qx 1
}

sleep 2
for step in "$@"; do
	focus
	case $step in
		wait) sleep 1; for _ in $(seq 600); do idle && break; sleep 1; done ;;
		sleep:*) sleep "${step#sleep:}" ;;
		key:*) wtype -k "${step#key:}" ;;
		type:*) wtype "${step#type:}" ;;
		/*) wtype "$step"; wtype -k Return ;;
		*) wtype "$step"; wtype -k Return; prompts=$((prompts + 1)) ;;
	esac
done
sleep 2

mkdir -p "$(dirname "$OUT")"
if [[ $OUT == *.gif ]]; then
	kill -INT "$REC_PID"; wait "$REC_PID" || true; REC_PID=
	mkdir "$TMP/frames"
	ffmpeg -loglevel error -i "$TMP/rec.mp4" -vf "fps=${GIF_FPS:-10}" "$TMP/frames/%05d.png"
	gifski --quiet --fps "${GIF_FPS:-10}" --width "${GIF_WIDTH:-1100}" -o "$OUT" "$TMP"/frames/*.png
else
	grim -g "$WIN" "$OUT"
fi
echo "wrote $OUT"
