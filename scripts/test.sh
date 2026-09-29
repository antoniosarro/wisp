#!/usr/bin/env bash
# Runs go test with its arguments (default ./...) and prints one line per
# package with test counts and coverage, then the failed tests' output
# (scripts/test.jq). The exit status is go test's.
set -uo pipefail
cd "$(dirname "$0")/.."

args=("$@")
# go test needs packages after the flags; add ./... unless one is given.
if ! printf '%s\n' "${args[@]}" | grep -q '^\./'; then
	args+=(./...)
fi

color=0
[ -t 1 ] && color=1
start=$(date +%s%N)
go test -json "${args[@]}" 2>&1 | jq -Rjn --arg color "$color" --arg module "$(go list -m)" -f scripts/test.jq
status=${PIPESTATUS[0]}
awk -v ns=$(($(date +%s%N) - start)) 'BEGIN { printf " in %.1fs\n", ns / 1e9 }'
exit "$status"
