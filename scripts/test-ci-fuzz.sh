#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Tests scripts/ci-fuzz.sh with a stand-in for "go test", which acts out what the fuzz engine can do.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
dir="$(mktemp -d)"
trap 'rm -rf "$dir"' EXIT
failed=0
ok() { printf 'ok   %s\n' "$1"; }
bad() { printf 'FAIL %s\n' "$1"; failed=1; }

mkdir "$dir/bin"
cat > "$dir/bin/go" <<'STUB'
#!/bin/bash
# The number of the call is kept in a file; SCRIPT says what each call does, one word per call.
count=$(cat "$STUBDIR/count" 2> /dev/null || echo 0)
count=$((count + 1))
echo "$count" > "$STUBDIR/count"
what=$(tr ' ' '\n' <<< "$SCRIPT" | sed -n "${count}p")
case "$what" in
	pass) echo "PASS"; exit 0 ;;
	deadline) echo "--- FAIL: FuzzX (20.14s)"; echo "    context deadline exceeded"; exit 1 ;;
	finding) echo "--- FAIL: FuzzX"; echo "    Failing input written to testdata/fuzz/FuzzX/abc"; exit 1 ;;
	panic) echo "panic: runtime error: index out of range"; exit 1 ;;
	other) echo "build failed"; exit 1 ;;
esac
exit 9
STUB
chmod +x "$dir/bin/go"

# run SCRIPT-OF-CALLS ARGS...: the status of ci-fuzz.sh, and how many times "go test" ran.
run() {
	local script=$1
	shift
	rm -f "$dir/count"
	STUBDIR="$dir" SCRIPT="$script" PATH="$dir/bin:$PATH" "$here/ci-fuzz.sh" "$@" > "$dir/out" 2>&1 && status=0 || status=$?
	calls=$(cat "$dir/count" 2> /dev/null || echo 0)
}

run "pass" FuzzX
if [ "$status" = 0 ] && [ "$calls" = 1 ]; then ok "a pass is a pass, run once"; else bad "a pass is a pass, run once ($status, $calls)"; fi

run "deadline pass" FuzzX
if [ "$status" = 0 ] && [ "$calls" = 2 ]; then ok "the engine's own deadline is repeated, and then passes"; else bad "the engine's own deadline is repeated ($status, $calls)"; fi

run "deadline deadline deadline" FuzzX
if [ "$status" = 1 ] && [ "$calls" = 3 ]; then ok "three deadlines in a row fail"; else bad "three deadlines in a row fail ($status, $calls)"; fi

run "finding pass" FuzzX
if [ "$status" = 1 ] && [ "$calls" = 1 ]; then ok "a finding is never repeated"; else bad "a finding is never repeated ($status, $calls)"; fi

run "panic pass" FuzzX
if [ "$status" = 1 ] && [ "$calls" = 1 ]; then ok "a panic is never repeated"; else bad "a panic is never repeated ($status, $calls)"; fi

run "other pass" FuzzX
if [ "$status" = 1 ] && [ "$calls" = 1 ]; then ok "a failure it does not know is not repeated"; else bad "a failure it does not know is not repeated ($status, $calls)"; fi

run "pass pass" FuzzA FuzzB
if [ "$status" = 0 ] && [ "$calls" = 2 ]; then ok "every target is run"; else bad "every target is run ($status, $calls)"; fi

run "pass finding" FuzzA FuzzB
if [ "$status" = 1 ] && [ "$calls" = 2 ]; then ok "a finding in the second target fails the run"; else bad "a finding in the second target fails the run ($status, $calls)"; fi

exit "$failed"
