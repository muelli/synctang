#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Creates, starts, stops and destroys a small EC2 instance for running the
# Android emulator with KVM (nested virtualization). See README.md.
#
#   emu-ctl.sh up [--dry-run]   create it, or start it if it is stopped
#   emu-ctl.sh status           state, type, address, whether setup finished
#   emu-ctl.sh ssh [cmd...]     a shell on it, or run one command
#   emu-ctl.sh wait-ready       wait until the first-boot setup has finished
#   emu-ctl.sh logs             the first-boot setup log
#   emu-ctl.sh keepalive        count as "in use" for another idle period
#   emu-ctl.sh phone-tunnel --usb | <host[:port]>
#                               let the VM's adb reach a phone: --usb tunnels
#                               this machine's adb server (a phone on USB), the
#                               other form a phone's own adb-over-network port
#   emu-ctl.sh stop             stop it now (disk kept, compute billing ends)
#   emu-ctl.sh destroy --yes    terminate it and delete everything it made
#
# It stops itself after EMU_IDLE_MINUTES (60) without use, see
# idle-shutdown.sh. Nothing here is secret: do not copy signing seeds or a
# LUKS passphrase onto the VM.
set -euo pipefail

NAME=${EMU_NAME:-synctang-emulator}
export AWS_PROFILE=${AWS_PROFILE:-code-poweruser}
export AWS_PAGER=""
REGION=${AWS_REGION:-${AWS_DEFAULT_REGION:-eu-central-1}}
INSTANCE_TYPES=${EMU_INSTANCE_TYPES:-"c8i.2xlarge c7i.2xlarge m8i.2xlarge m7i.2xlarge"}
VOLUME_GB=${EMU_VOLUME_GB:-100}
IDLE_MINUTES=${EMU_IDLE_MINUTES:-60}
MAX_RUNTIME_HOURS=${EMU_MAX_RUNTIME_HOURS:-10}
REPO_URL=${EMU_REPO_URL:-https://github.com/muelli/synctang}
REPO_REF=${EMU_REPO_REF:-main}
AMI_PARAM=/aws/service/canonical/ubuntu/server/26.04/stable/current/amd64/hvm/ebs-gp3/ami-id
KEY_NAME="$NAME-key"
SG_NAME="$NAME-sg"
AWS=${EMU_AWS:-aws}

die() { echo "emu-ctl: $*" >&2; exit 1; }
say() { echo "emu-ctl: $*" >&2; }
aws_() { "$AWS" --region "$REGION" "$@"; }

pubkey_file() {
	local f
	for f in "${EMU_SSH_PUBKEY:-}" "$HOME/.ssh/id_ed25519.pub" "$HOME/.ssh/id_rsa.pub"; do
		if [ -n "$f" ] && [ -r "$f" ]; then echo "$f"; return; fi
	done
	die "no ssh public key found; set EMU_SSH_PUBKEY or create ~/.ssh/id_ed25519"
}

ssh_base() {
	local pub key
	pub=$(pubkey_file)
	key=${pub%.pub}
	[ -r "$key" ] || die "the private key $key is not readable"
	echo ssh -i "$key" -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new \
		-o UserKnownHostsFile="$HOME/.ssh/known_hosts.emulator-vm" -o ServerAliveInterval=30
}

check_credentials() {
	aws_ sts get-caller-identity >/dev/null 2>&1 ||
		die "the AWS credentials for profile $AWS_PROFILE do not work; if the SSO session expired run: aws sso login --profile $AWS_PROFILE"
}

find_instance() {
	aws_ ec2 describe-instances \
		--filters "Name=tag:Name,Values=$NAME" "Name=instance-state-name,Values=pending,running,stopping,stopped" \
		--query 'Reservations[].Instances[].InstanceId' --output text | awk '{print $1}'
}

instance_state() {
	aws_ ec2 describe-instances --instance-ids "$1" --query 'Reservations[0].Instances[0].State.Name' --output text
}

instance_ip() {
	aws_ ec2 describe-instances --instance-ids "$1" --query 'Reservations[0].Instances[0].PublicIpAddress' --output text
}

# The first listed instance type that this region offers and that supports
# nested virtualization, which is what the emulator needs.
pick_instance_type() {
	local t offered features
	for t in $INSTANCE_TYPES; do
		offered=$(aws_ ec2 describe-instance-type-offerings --location-type region \
			--filters "Name=instance-type,Values=$t" \
			--query 'InstanceTypeOfferings[0].InstanceType' --output text 2>/dev/null || true)
		[ "$offered" = "$t" ] || continue
		features=$(aws_ ec2 describe-instance-types --instance-types "$t" \
			--query 'InstanceTypes[0].ProcessorInfo.SupportedFeatures' --output text 2>/dev/null || true)
		case "$features" in *nested-virtualization*) echo "$t"; return ;; esac
	done
	die "none of ($INSTANCE_TYPES) is offered in $REGION with nested virtualization; set EMU_INSTANCE_TYPES"
}

pick_network() { # prints "<vpc-id> <subnet-id>"
	local type=$1 vpc azs subnet
	if [ -n "${EMU_SUBNET_ID:-}" ] && [ -n "${EMU_VPC_ID:-}" ]; then
		echo "$EMU_VPC_ID $EMU_SUBNET_ID"
		return
	fi
	vpc=$(aws_ ec2 describe-vpcs --filters Name=is-default,Values=true --query 'Vpcs[0].VpcId' --output text)
	[ -n "$vpc" ] && [ "$vpc" != None ] || die "no default VPC in $REGION; set EMU_VPC_ID and EMU_SUBNET_ID"
	azs=$(aws_ ec2 describe-instance-type-offerings --location-type availability-zone \
		--filters "Name=instance-type,Values=$type" --query 'InstanceTypeOfferings[].Location' --output text)
	[ -n "$azs" ] || die "$type is not offered in any availability zone of $REGION"
	# shellcheck disable=SC2086 # azs is a whitespace-separated list on purpose
	subnet=$(aws_ ec2 describe-subnets \
		--filters "Name=vpc-id,Values=$vpc" "Name=default-for-az,Values=true" "Name=availability-zone,Values=${azs// /,}" \
		--query 'Subnets[0].SubnetId' --output text)
	[ -n "$subnet" ] && [ "$subnet" != None ] || die "no default subnet in $vpc for the zones that offer $type; set EMU_SUBNET_ID"
	echo "$vpc $subnet"
}

my_cidr() {
	local ip
	ip=${EMU_ALLOWED_CIDR:-}
	if [ -z "$ip" ]; then
		ip=$(curl -fsS -m 10 https://checkip.amazonaws.com | tr -d '[:space:]') || die "could not find this machine's public address; set EMU_ALLOWED_CIDR"
		ip="$ip/32"
	fi
	echo "$ip"
}

ensure_key_pair() {
	if aws_ ec2 describe-key-pairs --key-names "$KEY_NAME" >/dev/null 2>&1; then return; fi
	say "importing the ssh public key as $KEY_NAME"
	aws_ ec2 import-key-pair --key-name "$KEY_NAME" --public-key-material "fileb://$(pubkey_file)" \
		--tag-specifications "ResourceType=key-pair,Tags=[{Key=Name,Value=$KEY_NAME},{Key=Project,Value=$NAME}]" >/dev/null
}

ensure_security_group() { # prints the group id; leaves exactly one ssh rule, for $cidr
	local vpc=$1 cidr=$2 sg old
	sg=$(aws_ ec2 describe-security-groups --filters "Name=group-name,Values=$SG_NAME" "Name=vpc-id,Values=$vpc" \
		--query 'SecurityGroups[0].GroupId' --output text)
	if [ -z "$sg" ] || [ "$sg" = None ]; then
		say "creating the security group $SG_NAME"
		sg=$(aws_ ec2 create-security-group --group-name "$SG_NAME" --vpc-id "$vpc" \
			--description "ssh to the $NAME VM from one address" \
			--tag-specifications "ResourceType=security-group,Tags=[{Key=Name,Value=$SG_NAME},{Key=Project,Value=$NAME}]" \
			--query GroupId --output text)
	fi
	# Keep only the current address: it changes, and an old one should not stay open.
	# The backticks are JMESPath literals, not command substitution.
	# shellcheck disable=SC2016
	for old in $(aws_ ec2 describe-security-groups --group-ids "$sg" \
		--query 'SecurityGroups[0].IpPermissions[?FromPort==`22`].IpRanges[].CidrIp' --output text); do
		[ "$old" = "$cidr" ] && continue
		say "closing ssh for $old"
		aws_ ec2 revoke-security-group-ingress --group-id "$sg" --protocol tcp --port 22 --cidr "$old" >/dev/null
	done
	aws_ ec2 authorize-security-group-ingress --group-id "$sg" --protocol tcp --port 22 --cidr "$cidr" >/dev/null 2>&1 ||
		true # already open to this address
	echo "$sg"
}

render_user_data() {
	cat <<EOF
#!/bin/bash
# First-boot bootstrap: fetch the repository, then let it set the VM up.
set -euxo pipefail
exec > >(tee -a /var/log/emulator-setup.log) 2>&1
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq git
git clone --depth 1 --branch "$REPO_REF" "$REPO_URL" /opt/synctang
IDLE_MINUTES=$IDLE_MINUTES MAX_RUNTIME_HOURS=$MAX_RUNTIME_HOURS /opt/synctang/scripts/emulator-vm/vm-setup.sh
EOF
}

cmd_up() {
	local dry=""
	[ "${1:-}" = "--dry-run" ] && dry=--dry-run
	check_credentials
	local id state type net vpc subnet cidr sg ami ud
	local -a args
	id=$(find_instance)
	if [ -n "$id" ]; then
		state=$(instance_state "$id")
		case "$state" in
		running | pending) say "$id is already $state" ;;
		stopped)
			cidr=$(my_cidr)
			vpc=$(aws_ ec2 describe-instances --instance-ids "$id" --query 'Reservations[0].Instances[0].VpcId' --output text)
			ensure_security_group "$vpc" "$cidr" >/dev/null
			say "starting $id"
			aws_ ec2 start-instances --instance-ids "$id" >/dev/null
			;;
		*) die "$id is $state; try again in a minute" ;;
		esac
	else
		type=$(pick_instance_type)
		net=$(pick_network "$type")
		vpc=${net% *}
		subnet=${net#* }
		cidr=$(my_cidr)
		ami=$(aws_ ssm get-parameter --name "$AMI_PARAM" --query Parameter.Value --output text)
		say "creating a $type in $subnet ($ami), ssh allowed from $cidr only"
		args=(--image-id "$ami" --instance-type "$type" --subnet-id "$subnet" --associate-public-ip-address
			--cpu-options NestedVirtualization=enabled
			--instance-initiated-shutdown-behavior stop
			--metadata-options "HttpTokens=required,HttpEndpoint=enabled"
			--block-device-mappings "[{\"DeviceName\":\"/dev/sda1\",\"Ebs\":{\"VolumeSize\":$VOLUME_GB,\"VolumeType\":\"gp3\",\"Encrypted\":true,\"DeleteOnTermination\":true}}]"
			--tag-specifications "ResourceType=instance,Tags=[{Key=Name,Value=$NAME},{Key=Project,Value=$NAME}]"
			"ResourceType=volume,Tags=[{Key=Name,Value=$NAME},{Key=Project,Value=$NAME}]"
			--query 'Instances[0].InstanceId' --output text)
		if [ -n "$dry" ]; then
			# Creates nothing, so no key pair or security group is passed: this
			# checks the type, image, subnet, options and your permissions.
			args+=(--dry-run)
		else
			ensure_key_pair
			sg=$(ensure_security_group "$vpc" "$cidr")
			args+=(--key-name "$KEY_NAME" --security-group-ids "$sg")
		fi
		ud=$(mktemp)
		render_user_data > "$ud"
		id=$(aws_ ec2 run-instances "${args[@]}" --user-data "file://$ud" 2>&1) || true
		rm -f "$ud"
		if [ -n "$dry" ]; then
			case "$id" in
			*DryRunOperation*) echo "dry run: you are allowed to launch this instance" ;;
			*) die "dry run failed: $id" ;;
			esac
			return
		fi
		case "$id" in i-*) ;; *) die "run-instances failed: $id" ;; esac
		say "created $id"
	fi
	aws_ ec2 wait instance-running --instance-ids "$id"
	say "$id is running at $(instance_ip "$id")"
	say "first-boot setup takes about ten minutes the first time: emu-ctl.sh wait-ready"
}

cmd_status() {
	check_credentials
	local id
	id=$(find_instance)
	[ -n "$id" ] || { echo "no $NAME instance"; return; }
	echo "instance: $id"
	echo "state:    $(instance_state "$id")"
	echo "type:     $(aws_ ec2 describe-instances --instance-ids "$id" --query 'Reservations[0].Instances[0].InstanceType' --output text)"
	if [ "$(instance_state "$id")" = running ]; then
		echo "address:  $(instance_ip "$id")"
		# shellcheck disable=SC2046 # ssh_base prints a command line
		if $(ssh_base) -o ConnectTimeout=8 "ubuntu@$(instance_ip "$id")" 'test -e /var/lib/emulator-vm-ready' 2>/dev/null; then
			echo "setup:    finished"
		else
			echo "setup:    not finished, or ssh not reachable yet"
		fi
	fi
}

target() {
	check_credentials
	local id
	id=$(find_instance)
	[ -n "$id" ] || die "no $NAME instance; run: emu-ctl.sh up"
	[ "$(instance_state "$id")" = running ] || die "$id is not running; run: emu-ctl.sh up"
	echo "ubuntu@$(instance_ip "$id")"
}

cmd_ssh() {
	local t
	t=$(target)
	# shellcheck disable=SC2046 # ssh_base prints a command line
	exec $(ssh_base) "$t" "$@"
}

cmd_wait_ready() {
	local t i
	t=$(target)
	for i in $(seq 1 120); do
		# shellcheck disable=SC2046 # ssh_base prints a command line
		if $(ssh_base) -o ConnectTimeout=8 "$t" 'test -e /var/lib/emulator-vm-ready' 2>/dev/null; then
			say "ready"
			return
		fi
		[ "$i" -eq 1 ] && say "waiting for the first-boot setup (see: emu-ctl.sh logs)"
		sleep 15
	done
	die "still not ready after 30 minutes; see: emu-ctl.sh logs"
}

cmd_logs() {
	cmd_ssh 'sudo tail -n 60 /var/log/emulator-setup.log'
}

cmd_keepalive() {
	cmd_ssh 'sudo mkdir -p /run/emulator-vm && sudo touch /run/emulator-vm/keepalive && echo "kept alive for another idle period"'
}

cmd_phone_tunnel() {
	local phone=${1:-} t
	[ -n "$phone" ] || die "usage: emu-ctl.sh phone-tunnel --usb | <phone-host>[:port]   (port defaults to 5555)"
	t=$(target)
	if [ "$phone" = "--usb" ]; then
		say "forwarding VM port 15037 to this machine's adb server (port 5037); on the VM run: adb -P 15037 devices"
		say "close this (Ctrl-C) when you are done; a bare tunnel does not keep the VM running"
		# shellcheck disable=SC2046 # ssh_base prints a command line
		exec $(ssh_base) -N -R 15037:localhost:5037 "$t"
	fi
	case "$phone" in *:*) ;; *) phone="$phone:5555" ;; esac
	say "forwarding VM port 15555 to $phone; on the VM run: adb connect localhost:15555"
	say "close this (Ctrl-C) when you are done; a bare tunnel does not keep the VM running"
	# shellcheck disable=SC2046 # ssh_base prints a command line
	exec $(ssh_base) -N -R "15555:$phone" "$t"
}

cmd_stop() {
	check_credentials
	local id
	id=$(find_instance)
	[ -n "$id" ] || die "no $NAME instance"
	aws_ ec2 stop-instances --instance-ids "$id" >/dev/null
	say "stopping $id (the disk is kept)"
}

cmd_destroy() {
	[ "${1:-}" = "--yes" ] || die "this deletes the instance, its disk, its security group and its key pair; add --yes to confirm"
	check_credentials
	local id sg i
	id=$(find_instance)
	if [ -n "$id" ]; then
		say "terminating $id"
		aws_ ec2 terminate-instances --instance-ids "$id" >/dev/null
		aws_ ec2 wait instance-terminated --instance-ids "$id"
	fi
	sg=$(aws_ ec2 describe-security-groups --filters "Name=group-name,Values=$SG_NAME" \
		--query 'SecurityGroups[0].GroupId' --output text)
	if [ -n "$sg" ] && [ "$sg" != None ]; then
		for i in 1 2 3 4 5 6; do
			aws_ ec2 delete-security-group --group-id "$sg" >/dev/null 2>&1 && { say "deleted $sg"; break; }
			[ "$i" -eq 6 ] && say "could not delete $sg yet; run destroy again in a minute"
			sleep 10
		done
	fi
	aws_ ec2 delete-key-pair --key-name "$KEY_NAME" >/dev/null 2>&1 && say "deleted the key pair" || true
}

main() {
	local cmd=${1:-}
	[ $# -gt 0 ] && shift
	case "$cmd" in
	up) cmd_up "$@" ;;
	status) cmd_status ;;
	ssh) cmd_ssh "$@" ;;
	wait-ready) cmd_wait_ready ;;
	logs) cmd_logs ;;
	keepalive) cmd_keepalive ;;
	phone-tunnel) cmd_phone_tunnel "$@" ;;
	stop) cmd_stop ;;
	destroy) cmd_destroy "$@" ;;
	*) awk 'NR > 3 && /^#/ { print substr($0, 3) } NR > 3 && !/^#/ { exit }' "$0"; [ -z "$cmd" ] || exit 2 ;;
	esac
}

main "$@"
