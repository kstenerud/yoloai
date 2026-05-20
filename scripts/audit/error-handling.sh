#!/usr/bin/env bash
# ABOUTME: Reproduces the error-handling discipline check from the 2026-05
# ABOUTME: audit — counts wrapped/unwrapped fmt.Errorf calls and lists every
# ABOUTME: brittle strings.Contains(err.Error(), ...) site (F8).
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

echo "## fmt.Errorf totals (production code, excludes _test.go)"
errorf_total=$(grep -rn "fmt\.Errorf" --include="*.go" --exclude="*_test.go" . | wc -l)
errorf_wrapped=$(grep -rn "fmt\.Errorf" --include="*.go" --exclude="*_test.go" . | grep -c "%w" || true)
echo "  total:    $errorf_total"
echo "  with %w:  $errorf_wrapped"
if [ "$errorf_total" -gt 0 ]; then
  pct=$(awk -v w="$errorf_wrapped" -v t="$errorf_total" 'BEGIN{printf "%.0f", (w/t)*100}')
  echo "  wrap %:   ${pct}"
fi
echo
echo "## fmt.Errorf candidates for %v/%s-on-error (review manually)"
# Lines whose Errorf contains both `%[vs]` and an arg that mentions "err".
# Excludes %w lines (those wrap properly, %s after is stderr context, fine)
# and exit-code-then-stderr patterns ("exited with code %d: %s", ExitCode+stderr).
candidates=$(grep -rnE 'fmt\.Errorf\(.*%[vs]".*err' --include="*.go" --exclude="*_test.go" . \
  | grep -v "%w" \
  | grep -vE "exited with code|exit code" || true)
if [ -z "$candidates" ]; then echo "(none)"; else printf '%s\n' "$candidates"; fi
echo
echo "## panic() in production code"
grep -rn "^\s*panic(" --include="*.go" --exclude="*_test.go" . | wc -l
echo
echo "## Brittle err.Error() substring matches (F8 sites)"
echo
# Catches both direct strings.Contains(err.Error(), ...) and the two-step
# `msg := strings.ToLower(err.Error()); strings.Contains(msg, ...)` pattern.
direct=$(grep -rnE 'strings\.Contains\([^)]*\.Error\(\)' --include="*.go" --exclude="*_test.go" . || true)
twostep=$(grep -rnE '(strings\.ToLower\()?[^)]*\.Error\(\)\)?[[:space:]]*$' --include="*.go" --exclude="*_test.go" . \
  | grep -B0 "strings.Contains" || true)
matches=$(printf '%s\n%s\n' "$direct" "$twostep" | sed '/^$/d' | sort -u)
if [ -z "$matches" ]; then echo "(none)"; else printf '%s\n' "$matches"; fi
echo
echo "  Note: the only remaining sites should be the documented chokepoints"
echo "  in runtime/errs.go (W8 acceptance: 'irreducible at a chokepoint')."
