#!/usr/bin/env bash
# Assertion helpers shared by run.sh (host) and in-container.sh (inside the Linux test containers). Source it.
# Every failed check prints what it was and exits the whole test.

OUT=""
RC=0

step() { printf '== %s\n' "$*"; }
pass() { printf '   ok: %s\n' "$*"; }
fail() {
	printf '   FAIL: %s\n' "$*" >&2
	if [[ -n "$OUT" ]]; then
		printf '   ---- last command output ----\n%s\n   ----\n' "$OUT" >&2
	fi
	exit 1
}

# run_capture CMD...: runs CMD, keeps its stdout+stderr in OUT and its status in RC; never fails by itself.
run_capture() {
	set +e
	OUT="$("$@" 2>&1)"
	RC=$?
	set -e
}

# expect_ok NAME CMD...
expect_ok() {
	local name="$1"
	shift
	run_capture "$@"
	[[ "$RC" -eq 0 ]] || fail "$name: exit status $RC"
	pass "$name"
}

# expect_fail NAME PATTERN CMD...: CMD must fail and say PATTERN.
expect_fail() {
	local name="$1" pattern="$2"
	shift 2
	run_capture "$@"
	[[ "$RC" -ne 0 ]] || fail "$name: expected a failure, got exit status 0"
	[[ "$OUT" == *"$pattern"* ]] || fail "$name: the error does not mention '$pattern'"
	pass "$name"
}

out_has() {
	[[ "$OUT" == *"$1"* ]] || fail "output lacks '$1'"
	pass "output has '$1'"
}

out_lacks() {
	[[ "$OUT" != *"$1"* ]] || fail "output must not have '$1'"
	pass "output has no '$1'"
}

assert_eq() {
	[[ "$1" == "$2" ]] || fail "$3: expected '$2', got '$1'"
	pass "$3"
}

assert_file() { [[ -f "$1" ]] || fail "missing file $1"; pass "file $1"; }
assert_absent() { [[ ! -e "$1" && ! -L "$1" ]] || fail "$1 must not exist"; pass "absent $1"; }

file_has() {
	grep -qF -- "$2" "$1" || fail "$1 lacks '$2'"
	pass "$1 has '$2'"
}

file_lacks() {
	if grep -qF -- "$2" "$1"; then fail "$1 must not have '$2'"; fi
	pass "$1 has no '$2'"
}

mode_of() { stat -c %a "$1" 2>/dev/null || stat -f %Lp "$1"; }
owner_of() { stat -c %U "$1" 2>/dev/null || stat -f %Su "$1"; }

assert_mode() { assert_eq "$(mode_of "$1")" "$2" "mode of $1"; }
assert_owner() { assert_eq "$(owner_of "$1")" "$2" "owner of $1"; }

# wait_for SECONDS DESCRIPTION CMD...
wait_for() {
	local n="$1" what="$2" i=0
	shift 2
	while [[ "$i" -lt "$n" ]]; do
		if "$@" >/dev/null 2>&1; then
			pass "$what"
			return 0
		fi
		sleep 1
		i=$((i + 1))
	done
	fail "timed out after ${n}s: $what"
}
