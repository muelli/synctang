#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Tests emu-ctl.sh against a stand-in `aws`, so what it would ask AWS to do
# can be checked without an account. Every call the script makes is also
# given to the REAL aws CLI, aimed at an address nothing listens on, which
# checks the arguments the way the CLI checks them before it sends anything
# (shorthand syntax, JSON, JMESPath queries, option names). A malformed
# argument shows up here instead of on the first real run. Skipped, with a
# note, if the real aws CLI is not installed or SKIP_REAL_AWS_CHECK is set;
# CI sets it, so that an older aws CLI that does not know a newer option
# cannot fail the build for a reason unrelated to the script.
# The helpers below are called through check, which shellcheck cannot see.
# shellcheck disable=SC2329
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fail=0

real_aws=$(command -v aws || true)
[ -z "${SKIP_REAL_AWS_CHECK:-}" ] || real_aws=""
ssh-keygen -q -t ed25519 -N '' -f "$work/id_ed25519" >/dev/null

cat > "$work/aws" <<'SH'
#!/bin/bash
# Stand-in for aws. FAKE_INSTANCE (none|stopped|running), FAKE_OFFERED and
# FAKE_NESTED (lists of instance types) and FAKE_CREDS (ok|bad) steer it.
echo "$*" >> "$FAKE_DIR/calls.log"

# Validate the arguments with the real CLI, pointed at a dead endpoint. A
# call already validated is not validated again, which keeps the run short.
key=$(md5sum <<<"$*" | cut -d' ' -f1)
if [ -n "${REAL_AWS:-}" ] && [ ! -e "$FAKE_DIR/validated-$key" ]; then
	touch "$FAKE_DIR/validated-$key"
	# env -u, not AWS_PROFILE='': an empty profile name is itself an error,
	# raised before any argument is checked.
	err=$(env -u AWS_PROFILE AWS_ACCESS_KEY_ID=x AWS_SECRET_ACCESS_KEY=x AWS_MAX_ATTEMPTS=1 AWS_CONFIG_FILE=/dev/null \
		AWS_SHARED_CREDENTIALS_FILE=/dev/null AWS_EC2_METADATA_DISABLED=true \
		"$REAL_AWS" "$@" --endpoint-url http://127.0.0.1:9 --cli-connect-timeout 1 --cli-read-timeout 1 2>&1 >/dev/null || true)
	if grep -q -i -E "validation failed|unknown options|usage:|invalid choice|bad value|invalid json|error parsing|unable to parse|expected" <<<"$err"; then
		echo "$* => $err" >> "$FAKE_DIR/validation-errors.log"
	fi
fi

args=" $* "
sub=$(sed -E 's/^ *--region [^ ]+ //' <<<"$*" | cut -d' ' -f1-2)
case "$sub" in
"sts get-caller-identity") [ "${FAKE_CREDS:-ok}" = ok ] || { echo "Token has expired" >&2; exit 255; }; echo '{}' ;;
"ec2 describe-instances")
	if [[ $args == *"tag:Name"* ]]; then
		[ "${FAKE_INSTANCE:-none}" = none ] || echo "i-0abc"
	elif [[ $args == *State.Name* ]]; then echo "${FAKE_INSTANCE:-none}"
	elif [[ $args == *PublicIpAddress* ]]; then echo "203.0.113.10"
	elif [[ $args == *VpcId* ]]; then echo "vpc-1"
	elif [[ $args == *InstanceType* ]]; then echo "c7i.2xlarge"
	fi ;;
"ec2 describe-instance-type-offerings")
	if [[ $args == *"--location-type availability-zone"* ]]; then printf '%s\t%s\t%s\n' eu-central-1a eu-central-1c eu-central-1b
	else
		for t in $FAKE_OFFERED; do [[ $args == *"Values=$t "* ]] && { echo "$t"; exit 0; }; done
		echo None
	fi ;;
"ec2 describe-instance-types")
	for t in $FAKE_NESTED; do [[ $args == *"--instance-types $t "* ]] && { echo "nested-virtualization amx-bf16"; exit 0; }; done
	echo "" ;;
"ec2 describe-vpcs") echo "vpc-1" ;;
"ec2 describe-subnets")
	# Real EC2 matches the zone filter against whole zone names; a value
	# that still contains a tab or a space matches nothing.
	if [[ $args == *"Values=eu-central-1a,eu-central-1c,eu-central-1b "* ]]; then echo "subnet-1"; else echo None; fi ;;
"ssm get-parameter") echo "ami-1" ;;
"ec2 describe-key-pairs") exit 255 ;;
"ec2 describe-security-groups")
	if [[ $args == *"--group-ids"* ]]; then printf '%s\t%s\t%s\n' 198.51.100.1/32 192.0.2.7/32 203.0.113.99/32
	elif [ -e "$FAKE_DIR/sg-created" ]; then echo "sg-1"
	else echo None; fi ;;
"ec2 create-security-group") touch "$FAKE_DIR/sg-created"; echo "sg-1" ;;
"ec2 run-instances")
	for a in "$@"; do case "$a" in file://*) cp "${a#file://}" "$FAKE_DIR/userdata" ;; esac; done
	if [[ $args == *" --dry-run "* ]]; then echo "An error occurred (DryRunOperation) when calling the RunInstances operation: Request would have succeeded" >&2; exit 254; fi
	echo "i-0abc" ;;
*) : ;;
esac
exit 0
SH
chmod +x "$work/aws"

# run <args...>: runs emu-ctl.sh against the stand-in; output in $work/out, status in $rc.
run() {
	rm -f "$work/calls.log" "$work/validation-errors.log" "$work/userdata" "$work/sg-created"
	touch "$work/calls.log"
	set +e
	EMU_AWS="$work/aws" FAKE_DIR="$work" REAL_AWS="$real_aws" HOME="$work" \
		EMU_SSH_PUBKEY="$work/id_ed25519.pub" EMU_ALLOWED_CIDR=192.0.2.7/32 \
		FAKE_OFFERED="${FAKE_OFFERED-c7i.2xlarge m7i.2xlarge}" FAKE_NESTED="${FAKE_NESTED-c7i.2xlarge m7i.2xlarge}" \
		FAKE_INSTANCE="${FAKE_INSTANCE:-none}" FAKE_CREDS="${FAKE_CREDS:-ok}" \
		"$here/emu-ctl.sh" "$@" > "$work/out" 2>&1
	rc=$?
	set -e
}

check() { # check <description> <command...>: passes when the command succeeds
	local description=$1
	shift
	if "$@"; then echo "ok   $description"; else echo "FAIL $description"; fail=1; fi
}

called() { grep -q -- "$1" "$work/calls.log"; }
not_called() { ! grep -q -- "$1" "$work/calls.log"; }
said() { grep -q -- "$1" "$work/out"; }
succeeded() { [ "$rc" -eq 0 ]; }
failed() { [ "$rc" -ne 0 ]; }
no_validation_errors() { [ ! -s "$work/validation-errors.log" ] || { cat "$work/validation-errors.log" >&2; return 1; }; }
userdata_has() { grep -q -- "$1" "$work/userdata"; }

echo "== up, nothing exists yet"
run up
check "succeeds" succeeded
check "skips an instance type the region does not offer" called "--instance-type c7i.2xlarge"
check "turns nested virtualization on" called "--cpu-options NestedVirtualization=enabled"
check "makes a shutdown from inside stop the instance, not terminate it" called "--instance-initiated-shutdown-behavior stop"
check "requires IMDSv2" called "HttpTokens=required"
check "encrypts the disk" called '"Encrypted":true'
check "closes an ssh address that is no longer ours" called "revoke-security-group-ingress --group-id sg-1 --protocol tcp --port 22 --cidr 198.51.100.1/32"
check "opens ssh to our address only" called "authorize-security-group-ingress --group-id sg-1 --protocol tcp --port 22 --cidr 192.0.2.7/32"
check "does not open ssh to the world" not_called "0.0.0.0/0"
check "imports the ssh key" called "import-key-pair"
check "passes a first-boot script that fetches the repository" userdata_has "git clone --depth 1 --branch \"main\""
check "passes the idle limit to the setup" userdata_has "IDLE_MINUTES=60"
check "passes the runtime limit to the setup" userdata_has "MAX_RUNTIME_HOURS=10"
check "every call is accepted by the real aws CLI's argument checks" no_validation_errors

echo "== up with a different idle limit"
EMU_IDLE_MINUTES=30 run up
check "the idle limit reaches the first-boot script" userdata_has "IDLE_MINUTES=30"

echo "== up, instance already stopped"
FAKE_INSTANCE=stopped run up
check "starts it" called "start-instances --instance-ids i-0abc"
check "does not create a second one" not_called "run-instances"
check "every call is accepted by the real aws CLI's argument checks" no_validation_errors

echo "== up, instance already running"
FAKE_INSTANCE=running run up
check "starts nothing and creates nothing" not_called "start-instances"
check "creates nothing" not_called "run-instances"

echo "== up --dry-run"
run up --dry-run
check "reports that launching is allowed" said "you are allowed to launch"
check "asks EC2 for a dry run" called "run-instances .*--dry-run"
check "creates no key pair" not_called "import-key-pair"
check "creates no security group" not_called "create-security-group"
check "every call is accepted by the real aws CLI's argument checks" no_validation_errors

echo "== no usable instance type"
FAKE_NESTED="" run up
check "refuses" failed
check "says why" said "nested virtualization"
check "creates nothing" not_called "run-instances"

echo "== expired credentials"
FAKE_CREDS=bad run up
check "refuses" failed
check "tells the user how to log in" said "aws sso login"

echo "== stop"
FAKE_INSTANCE=running run stop
check "stops the instance" called "stop-instances --instance-ids i-0abc"

echo "== destroy"
FAKE_INSTANCE=running run destroy
check "refuses without --yes" failed
check "deletes nothing without --yes" not_called "terminate-instances"
FAKE_INSTANCE=running run destroy --yes
check "terminates the instance" called "terminate-instances --instance-ids i-0abc"
check "deletes the key pair" called "delete-key-pair --key-name synctang-emulator-key"
check "every call is accepted by the real aws CLI's argument checks" no_validation_errors

if [ -z "$real_aws" ]; then echo "note: the real aws CLI's argument checks were skipped (not installed, or SKIP_REAL_AWS_CHECK is set)"; fi
exit "$fail"
