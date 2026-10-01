#!/usr/bin/env bash
# Tags a release. Checks the staging area, asks wisp (on a local model) to
# suggest the next version and write the release notes from the commits
# since the last tag, then tags and pushes. The pushed tag starts the release
# workflow (.github/workflows/release.yml), which publishes the GitHub
# release with the packages: what turns on the update notice.
# Asks before every step; nothing leaves the machine without a yes.
#
# Usage: scripts/release.sh            (or: just release)
#   RELEASE_BASE_URL  OpenAI-compatible endpoint (default: local llama-swap)
#   RELEASE_MODEL     model on it (default: coder-35b)
set -euo pipefail

BASE_URL="${RELEASE_BASE_URL:-http://localhost:8091/v1}"
MODEL="${RELEASE_MODEL:-coder-35b}"

cd "$(dirname "$0")/.."
die() { echo "release: $*" >&2; exit 1; }
ask() { local a; read -rp "$1 [y/N] " a; [[ $a == [yY]* ]]; }

# 1. The staging area: a tag names a commit, so staged work must be
# committed first; unstaged work simply isn't part of the release.
if ! git diff --cached --quiet; then
    git diff --cached --stat
    ask "These changes are staged. Commit them first?" || die "commit or unstage them, then run again"
    git commit
fi
git diff --quiet || echo "release: note: unstaged changes are not part of the release" >&2

# 2. What changed since the last release.
last=$(git describe --tags --abbrev=0 --match 'v[0-9]*' 2>/dev/null || true)
range="${last:+$last..}HEAD"
[[ -z $last || -n $(git rev-list "$range") ]] || die "no commits since $last"
if [[ -n $last ]]; then
    changes="$(git log --no-merges --format='- %s' "$range")

$(git diff --stat "$last" HEAD | tail -40)

$(git diff "$last" HEAD -- . ':!*.png' ':!go.sum' | head -c 80000)"
else
    changes="$(git log --no-merges --format='- %s' | head -100)"
fi

# 3. Ask wisp for the bump and the notes. The key flag keeps any
# WISP_API_KEY meant for a remote provider away from the local endpoint.
go build -o bin/wisp ./cmd/wisp
out=$(mktemp)
notes=$(mktemp)
trap 'rm -f "$out" "$notes"' EXIT
echo "Asking $MODEL at $BASE_URL..."
./bin/wisp --base-url "$BASE_URL" --model "$MODEL" --api-key "" "You are preparing a release of wisp, this repository.
The last release is ${last:-none (this is the first)}. Below are the commits and changes since.
Commit messages may be uninformative (\"wip\"): judge from the diff, and read files if needed.

Choose the semantic version bump:
- major: breaking changes to command-line flags, config files, or the session format
- minor: new features
- patch: fixes only
While the version is 0.x, a breaking change is a minor bump, not major.

Reply with release notes for users: a short Markdown list under the headings
Added, Changed, and Fixed (omit empty ones), without a title, between a line
<release-notes> and a line </release-notes>. Then end with a line that is
exactly: BUMP: major, BUMP: minor, or BUMP: patch

$changes" | tee "$out"

# The reply as plain text: no colors, no usage line.
sed -e 's/\x1b\[[0-9;]*m//g' "$out" > "$out.plain" && mv "$out.plain" "$out"
bump=$(grep -oE '^BUMP: (major|minor|patch)' "$out" | tail -1 | cut -d' ' -f2 || true)
# The last <release-notes> block, so reasoning that precedes it is left out.
awk '/^<release-notes>$/ { buf = ""; on = 1; next }
     /^<\/release-notes>$/ { on = 0; notes = buf; next }
     on { buf = buf $0 "\n" }
     END { printf "%s", notes }' "$out" > "$notes"
[[ -s $notes ]] || die "wisp's reply had no <release-notes> block; see above"

# 4. The next version, from the last tag and the bump.
IFS=. read -r major minor patch <<< "${last#v}"
major=${major:-0} minor=${minor:-0} patch=${patch:-0}
case $bump in
    major) next="$((major + 1)).0.0" ;;
    minor) next="$major.$((minor + 1)).0" ;;
    patch) next="$major.$minor.$((patch + 1))" ;;
    *) echo "release: wisp suggested no bump; defaulting to minor" >&2; next="$major.$((minor + 1)).0" ;;
esac
[[ -n $last ]] || next=0.1.0

echo
read -rp "Version (last: ${last:-none}, suggested: $next${bump:+ ($bump)}): " version
version=${version:-$next}
version=${version#v}
[[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "$version is not X.Y.Z"
! git rev-parse -q --verify "refs/tags/v$version" >/dev/null || die "v$version exists already"
if ask "Edit the release notes first?"; then
    "${EDITOR:-vi}" "$notes"
fi

# 5. Tag (signed when git's tag.gpgSign is set), then the outward steps.
git tag -a "v$version" --cleanup=verbatim -F "$notes" # keeps "# Heading" lines
echo "Tagged v$version at $(git rev-parse --short HEAD)."

git remote get-url origin >/dev/null 2>&1 || { echo "No remote 'origin' yet: push with git push origin HEAD v$version"; exit 0; }
branch=$(git branch --show-current)
ask "Push $branch and v$version to origin?" || exit 0
git push origin "$branch" "v$version"
echo "GitHub Actions now builds the packages and publishes the release:"
echo "  https://github.com/antoniosarro/wisp/actions/workflows/release.yml"
