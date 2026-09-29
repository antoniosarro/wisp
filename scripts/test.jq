# Formats `go test -json` as one line per package, with test counts and
# coverage, and the output of each failed test (scripts/test.sh).
#   ✓ every test passed   ✖ something failed   ∅ no tests in the package
# Counts include subtests. $color is "1" for ANSI colors.

def c($code): if $color == "1" then "\u001b[\($code)m\(.)\u001b[0m" else . end;
def pad($n): . + (" " * ([$n - length, 1] | max));
def plural($n; $noun): "\($n) \($noun)" + (if $n == 1 then "" else "s" end);
def secs: . * 100 | round / 100 | tostring + "s";

def pkgline($k; $s; $e):
  (if $e.Action == "fail" then "✖" | c("31") elif $s.pass + $s.fail == 0 then "∅" | c("2") else "✓" | c("32") end) as $icon
  | ($k | ltrimstr($module + "/")) as $name
  | if $s.pass + $s.fail + $s.skip == 0 then "\($icon) \($name | pad(34))"
      + (if $e.Action == "fail" then "failed before any test ran" | c("31") else "no tests" | c("2") end)
    else
      "\($icon) \($name | pad(34))\(plural($s.pass + $s.fail; "test") | pad(10))"
      + (if $s.fail > 0 then (" \($s.fail) failed" | c("31")) else "" end)
      + (if $s.skip > 0 then (" \($s.skip) skipped" | c("33")) else "" end)
      + (if $s.cov != "" then "  \($s.cov | pad(6))" else "" end)
      + ("  " + (if $s.cached then "cached" else $e.Elapsed // 0 | secs end) | c("2"))
    end;

# Input is raw lines: go test's JSON, and anything else (compiler errors
# before -json takes over) shown as is.
foreach ((inputs | . as $l | try fromjson catch {Action: "build-output", Output: $l}), {Action: "end"}) as $e (
  {p: {}, total: {pass: 0, fail: 0, skip: 0}, emit: null};
  .emit = null
  | if $e.Action == "end" then
      .emit = "\n" + (
        "\(plural(.total.pass + .total.fail; "test")): \(.total.pass) passed"
        + (if .total.fail > 0 then (", \(.total.fail) failed" | c("31")) else "" end)
        + (if .total.skip > 0 then ", \(.total.skip) skipped" else "" end))
    elif $e.Action == "build-output" then .emit = ($e.Output | rtrimstr("\n") | c("31"))
    elif $e.Package == null then .
    else
      $e.Package as $k
      | .p[$k] //= {pass: 0, fail: 0, skip: 0, cov: "", out: {}}
      | if $e.Test != null then
          if $e.Action == "output" then .p[$k].out[$e.Test] += [$e.Output]
          elif ($e.Action | IN("pass", "fail", "skip")) then
            .p[$k][$e.Action] += 1 | .total[$e.Action] += 1
            | if $e.Action == "fail" then .p[$k].failed += [.p[$k].out[$e.Test] | add // ""] else . end
            | del(.p[$k].out[$e.Test])
          else . end
        elif $e.Action == "output" then
          (if $e.Output | test("^ok .*\\(cached\\)") then .p[$k].cached = true else . end)
          | (($e.Output | capture("coverage: (?<c>[0-9.]+%)").c) as $cov | .p[$k].cov = $cov)
            // (.p[$k].out[""] += [$e.Output])
        elif ($e.Action | IN("pass", "fail", "skip")) then
          .p[$k] as $s
          | .emit = ([pkgline($k; $s; $e)]
              + ($s.failed // [] | map(rtrimstr("\n") | split("\n") | map("    " + .) | join("\n")))
              # a package that failed with no failed test: a panic, a TestMain exit
              + (if $e.Action == "fail" and $s.fail == 0 then [$s.out[""] // [] | add // "" | rtrimstr("\n")] else [] end)
              | join("\n"))
          | del(.p[$k])
        else . end
    end;
  .emit // empty | . + (if $e.Action == "end" then "" else "\n" end)
)
