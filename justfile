# packages for Linux distributions and Nix: just pkg <recipe> (packaging/README.md)
mod pkg 'packaging'

default:
    @just --list

# compile the binary to bin/wisp
build:
    go build -o bin/wisp ./cmd/wisp

# build and run the app, forwarding any extra arguments
run *args:
    go run ./cmd/wisp {{args}}

# run tests, forwarding any extra flags (e.g. -v, -run TestFoo)
test *args:
    scripts/test.sh {{args}}

# run tests with the race detector enabled
race *args:
    scripts/test.sh -race {{args}}

# run tests with coverage and print the overall coverage percentage;
# pass html=1 to also open an HTML coverage report in the browser
cover html="":
    scripts/test.sh -coverprofile=coverage.out
    @go tool cover -func=coverage.out | tail -1 | tr -s '\t' ' '
    {{ if html != "" { "go tool cover -html=coverage.out" } else { "" } }}

# end-to-end: scripted scenarios against a fake model, and the TUI in a terminal
e2e:
    go test ./internal/cli -run 'TestScripts|TestTUIInTerminal' -v

# TUI screen snapshots; pass update=1 to regenerate them after an intended change
snapshots update="":
    go test ./internal/tui -run 'TestSnapshots|TestScreenFitsAnySize|TestLayoutsFillTheScreen' {{ if update != "" { "-update" } else { "" } }}

# fuzz the TUI layout across terminal sizes and states
fuzz time="30s":
    go test ./internal/tui -run '^$' -fuzz FuzzScreenFits -fuzztime {{time}}

# screenshot or GIF of the real TUI, in a private headless display: just screenshot OUT.png|OUT.gif [STEP...] (scripts/screenshot.sh)
[positional-arguments]
screenshot *args:
    ./scripts/screenshot.sh "$@"

# record the docs' GIFs on OpenRouter in a private headless display: just gifs [NAME...] (scripts/gifs.sh)
[positional-arguments]
gifs *args:
    ./scripts/gifs.sh "$@"

# tag a release: wisp on a local model suggests the version and notes (scripts/release.sh)
release:
    ./scripts/release.sh

# write a Conventional Commits message for the staged changes with the local model (scripts/commit-msg.sh), then review it in git's editor
commit:
    @msg="$(scripts/commit-msg.sh)" && git commit --edit -m "$msg"

# only print the message the model would write for the staged changes
commit-msg:
    @scripts/commit-msg.sh

# format Go source files in place with gofmt
fmt:
    gofmt -l -w .

# run go vet on all packages to detect potential issues
vet:
    go vet ./...

# run golangci-lint (all configured linters)
lint:
    golangci-lint run

# run fmt + vet + lint + test, the full pre-commit check
check: fmt vet lint test sprites-check

# tidy go.mod and go.sum to match the current source tree
tidy:
    go mod tidy

# One <tag>.png of 128px frames per tag (--split-tags leaves {tag} empty in 1.3).
# export assets/mascot/mascot.aseprite into the embedded mascot strips
sprites dir="assets/mascot":
    for t in $(aseprite -b --list-tags assets/mascot/mascot.aseprite); do \
        aseprite -b --tag $t assets/mascot/mascot.aseprite --scale 2 \
            --sheet-type horizontal --sheet {{dir}}/$t.png >/dev/null; \
    done

# The PNGs are committed because every build embeds them, but only a machine
# with Aseprite can make them, so edits to the source are easy to forget.
# fail if the mascot strips differ from a fresh export (skipped without Aseprite)
sprites-check:
    #!/usr/bin/env bash
    set -euo pipefail
    command -v aseprite >/dev/null || { echo "sprites-check: no aseprite, skipped"; exit 0; }
    tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
    mkdir "$tmp/new" "$tmp/old"
    just sprites "$tmp/new"
    cp assets/mascot/*.png "$tmp/old"
    diff -rq "$tmp/old" "$tmp/new" || { echo "assets/mascot: the PNGs are out of date; run just sprites, and delete strips whose tag is gone"; exit 1; }
