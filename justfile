default:
    @just --list

build:
    go build -o bin/wisp ./cmd/wisp

run *args:
    go run ./cmd/wisp {{args}}

# write a Conventional Commits message for the staged changes with the local model (scripts/commit-msg.sh), then review it in git's editor
commit:
    @msg="$(scripts/commit-msg.sh)" && git commit --edit -m "$msg"

# only print the message the model would write for the staged changes
commit-msg:
    @scripts/commit-msg.sh

fmt:
    gofmt -l -w .

vet:
    go vet ./...

lint:
    golangci-lint run

check: fmt vet lint

tidy:
    go mod tidy