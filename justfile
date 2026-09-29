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
check: fmt vet lint test

# tidy go.mod and go.sum to match the current source tree
tidy:
    go mod tidy