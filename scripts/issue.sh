#!/usr/bin/env bash
# GitHub issues for this repository, through the GitHub REST API.
#
#   scripts/issue.sh new [--remote] [TEXT...]   (or: just issue [--remote] [TEXT...])
#     wisp, on a local model, turns a description or a pasted error into an
#     issue: it reads the code to find the cause, and drops keys, tokens and
#     account ids, and picks labels from the repository's. You edit the
#     draft, then confirm before it is posted.
#     TEXT is the description, and stdin, when it isn't a terminal, the
#     evidence: just issue "what I did" < error.log. Either one alone does;
#     with neither, $EDITOR opens for it. Name larger files by their path
#     in TEXT, for wisp to read.
#   scripts/issue.sh get NUMBER       (or: just issue-get NUMBER)
#     prints an issue, its labels and its comments.
#   scripts/issue.sh labels           (or: just issue-labels)
#     creates the labels below that the repository lacks, after asking.
#
# Keys: the first command that needs one asks for it, with where to make
# it, and saves it under ~/.config/wisp, readable only by you.
#   GITHUB_TOKEN    a fine-grained token for this repository with Issues:
#                   read and write; needed to post and to create labels
#   GITHUB_TOKEN_FILE  a file holding it instead, such as a sops-nix secret
#                   under /run/secrets (default: ~/.config/wisp/github-token)
#   ISSUE_BASE_URL  OpenAI-compatible endpoint (default: local llama-swap)
#   ISSUE_MODEL     model on it (default: coder-35b)
#   --remote        use OpenRouter instead: a more capable model, which the
#                   report (redacted) and the code it reads are sent to
#   OPENROUTER_KEY_FILE  the file holding the OpenRouter key, for --remote
#                   (default: $WISP_QA_KEY_FILE, as scripts/gifs.sh uses,
#                   else ~/.config/wisp/openrouter-key)
#   ISSUE_REMOTE_MODEL  the model on OpenRouter (default: z-ai/glm-5.3-flash)
set -euo pipefail

BASE_URL="${ISSUE_BASE_URL:-http://localhost:8091/v1}"
MODEL="${ISSUE_MODEL:-coder-35b}"
cheapest=()

cd "$(dirname "$0")/.."
source scripts/secret.sh
die() { echo "issue: $*" >&2; exit 1; }
# Answers come from the terminal, since stdin may have been the
# description; with none, every answer is no.
tty=/dev/tty
: 2>/dev/null </dev/tty || tty=/dev/null
ask() { local a=; read -rp "$1 [y/N] " a <"$tty" || true; [[ $a == [yY]* ]]; }
# redact replaces what looks like a secret or an account id on stdin: API
# keys and tokens, long hex ids (48 digits and up, which spares commit
# hashes), OpenRouter user ids, emails, and home directories. The model is
# asked to do the same, but can't be relied on.
redact() {
    sed -E -e 's/(sk|pk|rk)-[A-Za-z0-9_-]{16,}/<redacted>/g' \
        -e 's/(gh[pousr]_|github_pat_)[A-Za-z0-9_]{20,}/<redacted>/g' \
        -e 's/(Bearer|token=|key=) *[A-Za-z0-9._~+\/-]{16,}/\1 <redacted>/gI' \
        -e 's/\b[0-9a-fA-F]{48,}\b/<redacted>/g' \
        -e 's/\buser_[A-Za-z0-9]{16,}/<redacted>/g' \
        -e 's/[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}/<redacted>/g' \
        -e 's#/home/[^/[:space:]]+#~#g'
}

# owner/name, from an SSH or HTTPS origin.
repo=$(git remote get-url origin 2>/dev/null | sed -E 's#^(git@github\.com:|https://github\.com/)##; s#\.git$##')
[[ $repo =~ ^[^/:]+/[^/]+$ ]] || die "origin is not a GitHub repository"
# The token, out of the environment: wisp runs below, and its bash tool
# would pass it to every command the model runs. need_token loads it, or
# asks for it, when a command needs one.
token=${GITHUB_TOKEN:-} token_file=${GITHUB_TOKEN_FILE:-}
unset GITHUB_TOKEN GITHUB_TOKEN_FILE
token_default=$secret_dir/github-token
need_token() {
    [[ -z $token ]] || return 0
    token=$(secret_file issue GITHUB_TOKEN_FILE "$token_file" "$token_default" \
        "GitHub token needed: create a fine-grained one at https://github.com/settings/personal-access-tokens/new with access to only $repo and the permission Issues: Read and write.") || exit 1
}
# api METHOD PATH [CURL-ARGS...]: a call on this repository; GitHub's error
# message is printed when it fails. The token header comes from a file
# descriptor, not curl's arguments, which any user can list.
api() {
    curl -sS --fail-with-body -X "$1" "https://api.github.com/repos/$repo$2" \
        -H @<([[ -z $token ]] || printf 'Authorization: Bearer %s\n' "$token") \
        -H 'Accept: application/vnd.github+json' -H 'X-GitHub-Api-Version: 2022-11-28' "${@:3}"
}
# gh_error JSON: GitHub's message in an error reply, and where a rejected
# token came from.
gh_error() {
    local msg
    msg=$(jq -r '.message // .' <<< "$1" 2>/dev/null || echo "$1")
    [[ $msg != "Bad credentials" ]] || msg+=" (the token in ${token_file:-$token_default}, or \$GITHUB_TOKEN; replace it with a valid one)"
    echo "$msg"
}

# The labels the repository should have, beyond GitHub's defaults (bug,
# enhancement, documentation, question, ...): name|color|description.
extra_labels='feature|0e8a16|A new capability
nice-to-have|c5def5|Would help, but can wait
security|b60205|A security weakness, or hardening against one
performance|fbca04|Speed or resource use'

# pick_labels WANTED HAVE: the comma-separated WANTED names found in the
# newline-separated HAVE, spelled as there, as {keep: [...], drop: [...]}.
pick_labels() {
    jq -nc --arg want "$1" --arg have "$2" '
        ($have | split("\n")) as $h
        | [$want | split(",")[] | gsub("^\\s+|\\s+$"; "") | select(. != "")]
        | map(. as $w | {w: $w, h: ([$h[] | select(ascii_downcase == ($w | ascii_downcase))] | first)})
        | {keep: ([.[] | select(.h) | .h] | unique), drop: [.[] | select(.h | not) | .w]}'
}

case ${1:-} in
labels)
    need_token
    have=$(api GET "/labels?per_page=100") || die "listing the labels: $(gh_error "$have")"
    have=$(jq -r '.[].name' <<< "$have")
    missing=$(while IFS='|' read -r name color desc; do
        grep -qixF "$name" <<< "$have" || echo "$name|$color|$desc"
    done <<< "$extra_labels")
    [[ -n $missing ]] || { echo "$repo has all the labels already."; exit; }
    echo "Missing from $repo:"
    cut -d'|' -f1,3 <<< "$missing" | sed 's/|/: /; s/^/  /'
    ask "Create them?" || exit 0
    while IFS='|' read -r name color desc; do
        res=$(jq -n --arg name "$name" --arg color "$color" --arg description "$desc" '{$name, $color, $description}' |
            api POST /labels --data-binary @-) || die "creating $name: $(gh_error "$res")"
        echo "Created $name"
    done <<< "$missing"
    exit
    ;;
get)
    [[ ${2:-} =~ ^[0-9]+$ ]] || die "usage: issue.sh get NUMBER"
    # Public issues need no token: use one only if it is already there.
    [[ -z $token_file && ! -f $token_default ]] || need_token
    issue=$(api GET "/issues/$2") || die "issue #$2: $(gh_error "$issue")"
    jq -r '"#\(.number) \(.title) [\(.state)]\n\(.html_url)\nby \(.user.login), \(.created_at)\(if .labels != [] then "\nlabels: " + ([.labels[].name] | join(", ")) else "" end)\n\n\(.body // "")"' <<< "$issue"
    api GET "/issues/$2/comments?per_page=100" | jq -r '.[] | "\n--- \(.user.login), \(.created_at)\n\(.body)"'
    exit
    ;;
new)
    shift
    need_token
    if [[ ${1:-} == --remote ]]; then
        shift
        BASE_URL=https://openrouter.ai/api/v1 MODEL=${ISSUE_REMOTE_MODEL:-z-ai/glm-5.3-flash}
        cheapest=(--cheapest) # the model's two cheapest zero-retention providers
        key=$(secret_file "issue: --remote" OPENROUTER_KEY_FILE "${OPENROUTER_KEY_FILE:-${WISP_QA_KEY_FILE:-}}" \
            "$secret_dir/openrouter-key" "OpenRouter key needed: create one at https://openrouter.ai/settings/keys; a credit limit keeps a mistake cheap.") || exit 1
    fi
    ;;
*) die "usage: issue.sh new [--remote] [TEXT...] | get NUMBER | labels" ;;
esac

out=$(mktemp)
draft=$(mktemp --suffix=.md)
trap 'rm -f "$out" "$draft"' EXIT

# 1. The description.
report="$*"
if [[ ! -t 0 ]]; then
    evidence=$(cat)
    [[ -z $evidence ]] || report="${report:+$report

Evidence:
}$evidence"
elif [[ -z $report ]]; then
    printf '\n# Describe the problem or paste the error above. Lines starting with # are dropped.\n' > "$draft"
    "${EDITOR:-vi}" "$draft"
    report=$(grep -v '^#' "$draft" || true)
fi
[[ -n ${report//[[:space:]]/} ]] || die "no description"
# The prompt is one argument, which Linux caps at 128 KB.
((${#report} < 100000)) || die "the description is ${#report} bytes; trim it, or name the file by its path for wisp to read"
report=$(redact <<< "$report")

# 2. Ask wisp for the draft, with the labels it may pick from.
labels=$(api GET "/labels?per_page=100") || die "listing the labels: $(gh_error "$labels")"
label_names=$(jq -r '.[].name' <<< "$labels")
label_list=$(jq -r '.[] | "- \(.name): \(.description // "")"' <<< "$labels")
# The key goes to wisp alone, which keeps it from the commands it runs;
# empty for the local endpoint, so a WISP_API_KEY meant for a remote
# provider stays away from it.
go build -o bin/wisp ./cmd/wisp
echo "Asking $MODEL at $BASE_URL..."
WISP_API_KEY=${key:-} ./bin/wisp --base-url "$BASE_URL" --model "$MODEL" "${cheapest[@]}" "You are filing a GitHub issue for wisp, this repository.
Below is what the user reported: a description, an error, or both.

Read the code to find where the problem comes from, and cite files as
path:line. Say what is known and what is a guess; don't invent versions,
models or steps the report doesn't give: leave a <placeholder> instead.

Remove anything private: API keys, tokens, URLs that carry a key or account
id, user ids, emails, and paths under a home directory. Write <redacted>.

Reply with the title on a line between <issue-title> and </issue-title>,
then the body in GitHub Markdown between a line <issue-body> and a line
</issue-body>. The body has the sections: What happens, Why (the cause in
the code), Expected, Possible fix, Environment. Don't use em dashes (—):
write a comma, a colon, parentheses, or a new sentence instead.

Last, pick one to three labels for the issue, only from the list below, and
put them comma-separated on a line between <issue-labels> and </issue-labels>.
Use one for the kind (bug, feature, enhancement, documentation, question),
and add others only when they clearly fit (nice-to-have when it can wait).
$label_list

Report:
$report" | tee "$out"

# The reply as plain text, then the text of its last <TAG>...</TAG>, so
# reasoning that precedes it is left out. Models put the tags on lines of
# their own or not, and wisp's usage line may follow the closing one.
sed -i -e 's/\x1b\[[0-9;]*m//g' "$out"
last_tag() {
    awk -v tag="$1" '{ s = s $0 "\n" }
        END {
            open = "<" tag ">"; p = 0
            while ((i = index(substr(s, p + 1), open)) > 0) p += i
            if (!p) exit
            s = substr(s, p + length(open)); j = index(s, "</" tag ">")
            if (!j) exit
            s = substr(s, 1, j - 1); gsub(/^[[:space:]]+|[[:space:]]+$/, "", s)
            print s
        }' "$out"
}
# Em dashes the model wrote anyway: a comma between words, a hyphen
# elsewhere (a list item's "- **x** —", a table cell).
nodash() { sed -E -e 's/([^|[:space:]]) — ([^|[:space:]])/\1, \2/g' -e 's/—/-/g'; }
title=$(last_tag issue-title | paste -sd ' ' | nodash)
last_tag issue-body | nodash > "$draft"
[[ -n $title && -s $draft ]] || die "wisp's reply had no <issue-title> or <issue-body>; see above"
picked=$(pick_labels "$(last_tag issue-labels | paste -sd ',')" "$label_names")

# 3. Review: the first line is the title, the second the labels. Redacted
# again after the edit, since a paste while editing can bring a secret back.
{ echo "$title"; echo "Labels: $(jq -r '.keep | join(", ")' <<< "$picked")"; echo; cat "$draft"; } > "$out" && mv "$out" "$draft"
echo
if ask "Edit the issue first?"; then
    "${EDITOR:-vi}" "$draft"
fi
redact < "$draft" > "$out"
cmp -s "$draft" "$out" || { echo "Redacted after the edit:"; diff "$draft" "$out" | grep '^>' || true; }
mv "$out" "$draft"
title=$(head -1 "$draft")
label_line=$(sed -n 2p "$draft")
if [[ $label_line == Labels:* ]]; then
    body=$(tail -n +4 "$draft")
else
    label_line= body=$(tail -n +3 "$draft")
fi
[[ -n $title ]] || die "the title (first line) is empty"
picked=$(pick_labels "${label_line#Labels:}" "$label_names")
dropped=$(jq -r '.drop | join(", ")' <<< "$picked")
[[ -z $dropped ]] || echo "issue: not labels of $repo, left out: $dropped (just issue-labels creates the usual ones)" >&2

echo
echo "Title: $title"
echo "Labels: $(jq -r '.keep | if . == [] then "none" else join(", ") end' <<< "$picked")"
ask "Post this issue to $repo?" || { cp "$draft" ./issue-draft.md; echo "Saved to issue-draft.md."; exit 0; }
res=$(jq -n --arg title "$title" --arg body "$body" --argjson labels "$(jq -c .keep <<< "$picked")" '{$title, $body, $labels}' |
    api POST /issues --data-binary @-) || { cp "$draft" ./issue-draft.md; die "posting: $(gh_error "$res"); the draft is saved to issue-draft.md"; }
jq -r '"Posted #\(.number): \(.html_url)"' <<< "$res"
