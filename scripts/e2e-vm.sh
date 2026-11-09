#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# End to end: the packages, in a real machine, unlocked by a key holder that is not
# on it. Builds a throwaway VM with a LUKS2 root, installs the .deb packages in it,
# enrols a laptop key holder and a headless phone, reboots it to its passphrase
# prompt, and unlocks it twice, once from each, with nobody typing the passphrase.
# It also checks what must not work: a key holder that was never enrolled gets nowhere.
#
# The relay and the discovery server are real Syncthing ones, on a private bridge
# that has no route out, so nothing depends on the public servers, and nothing
# says no to a CI run because they are busy.
#
# What it does not cover: the Android app. The phone here is e2ephone, which is the
# app's Go library with a key in a file; the Keystore, the fingerprint and the
# Kotlin screens are tried by hand on a phone.
#
# Needs root (a bridge, loop devices, chroot), KVM, qemu, dnsmasq, cryptsetup, sgdisk
# and the three .deb packages (synctang-unlocker, synctang-dracut, synctang-keyholder)
# in $DEBS, which "just deb" makes. strelaysrv and stdiscosrv come from
# $STRELAYSRV and $STDISCOSRV (scripts/build-syncthing-servers.sh builds them).
#
# Everything lives under $WORK; its logs are what to look at when it fails.

set -euo pipefail

# dnsmasq, cryptsetup and sgdisk are in the sbin directories, which "sudo env PATH=..." drops.
export PATH="$PATH:/usr/local/sbin:/usr/sbin:/sbin"

REPO="$(cd "$(dirname "$0")/.." && pwd)"
WORK="${WORK:-/var/tmp/synctang-e2e}"
DEBS="${DEBS:-$REPO/dist}"
# The passphrase is a fixture of a disk that holds nothing; see make-test-vm.sh.
VM_PASSPHRASE="${VM_PASSPHRASE:-synctang-test-passphrase}"
BRIDGE_IP=10.77.0.1
SSH_PORT="${SSH_PORT:-7522}"
CONSOLE_PORT="${CONSOLE_PORT:-7500}"
JOURNAL_PORT="${JOURNAL_PORT:-7501}"
export SSH_PORT CONSOLE_PORT JOURNAL_PORT

RUN="$WORK/run"
VMDIR="$RUN/vm"
LOGS="$RUN/logs"
CONSOLE_LOG="$LOGS/console.log"
FIFO="$RUN/console.in"

FAILED=0
PIDS=()

log() { printf '\n=== %s\n' "$*" >&2; }
fail() { printf 'e2e: %s\n' "$*" >&2; exit 1; }

# check NAME COMMAND...: an assertion. The run goes on after a failed one, so that one
# run shows everything that is wrong.
check() {
	local name=$1
	shift
	if "$@"; then
		printf 'ok   %s\n' "$name"
	else
		printf 'FAIL %s\n' "$name"
		FAILED=1
	fi
}

# shellcheck disable=SC2329  # run by the trap
cleanup() {
	set +e
	for pid in "${PIDS[@]}"; do
		kill "$pid" 2> /dev/null
	done
	if [ -f "$VMDIR/qemu.pid" ]; then
		kill "$(cat "$VMDIR/qemu.pid")" 2> /dev/null
	fi
	"$REPO/scripts/test-vm-net.sh" down > /dev/null 2>&1
}
trap cleanup EXIT

[ "$(id -u)" -eq 0 ] || fail "must run as root (a bridge, loop devices and a chroot)"
[ -n "${STRELAYSRV:-}" ] && [ -x "$STRELAYSRV" ] || fail "set STRELAYSRV to strelaysrv (scripts/build-syncthing-servers.sh)"
[ -n "${STDISCOSRV:-}" ] && [ -x "$STDISCOSRV" ] || fail "set STDISCOSRV to stdiscosrv (scripts/build-syncthing-servers.sh)"
if [ ! -w /dev/kvm ] && [ -z "${E2E_ALLOW_TCG:-}" ]; then
	fail "no usable /dev/kvm: without it the VM is too slow to test anything (E2E_ALLOW_TCG=1 to try anyway)"
fi

latest() {
	local found
	found="$(find "$DEBS" -maxdepth 1 -name "$1" | sort -V | tail -1)"
	[ -n "$found" ] || fail "no $1 in $DEBS (run: just deb)"
	printf '%s' "$found"
}
UNLOCKER_DEB="$(latest 'synctang-unlocker_*_amd64.deb')"
DRACUT_DEB="$(latest 'synctang-dracut_*_all.deb')"
KEYHOLDER_DEB="$(latest 'synctang-keyholder_*_amd64.deb')"

mkdir -p "$WORK/cache" "$VMDIR" "$LOGS" "$RUN/servers/disco" "$RUN/servers/relay" "$RUN/bin"
rm -f "$CONSOLE_LOG" "$FIFO" "$VMDIR/qemu.pid" "$VMDIR/disk.img"
: > "$CONSOLE_LOG"
mkfifo "$FIFO"

# ---------------------------------------------------------------------------------
log "a private network with a relay and a discovery server of its own"
"$REPO/scripts/test-vm-net.sh" up

(cd "$RUN/servers" && exec "$STDISCOSRV" --cert disco/cert.pem --key disco/key.pem \
	--listen "$BRIDGE_IP:8443" --db-dir disco > "$LOGS/stdiscosrv.log" 2>&1) &
PIDS+=("$!")
(cd "$RUN/servers" && exec "$STRELAYSRV" -keys relay -listen "$BRIDGE_IP:22067" -pools= -status-srv= \
	> "$LOGS/strelaysrv.log" 2>&1) &
PIDS+=("$!")

DISCO_ID="" RELAY_ID=""
for _ in $(seq 1 30); do
	DISCO_ID="$(sed -n 's/.*Server device ID is \([A-Z0-9-]*\).*/\1/p' "$LOGS/stdiscosrv.log" | head -1)"
	RELAY_ID="$(sed -n 's/.*URI: relay:[^ ]*[?]id=\([A-Z0-9-]*\).*/\1/p' "$LOGS/strelaysrv.log" | head -1)"
	[ -n "$DISCO_ID" ] && [ -n "$RELAY_ID" ] && break
	sleep 1
done
for pid in "${PIDS[@]}"; do
	kill -0 "$pid" 2> /dev/null || fail "a server died at start (is the port taken? see $LOGS/stdiscosrv.log and $LOGS/strelaysrv.log)"
done
[ -n "$DISCO_ID" ] || fail "stdiscosrv never said its Device ID (see $LOGS/stdiscosrv.log)"
[ -n "$RELAY_ID" ] || fail "strelaysrv never said its URI (see $LOGS/strelaysrv.log)"
DISCOVERY="https://$BRIDGE_IP:8443/v2/?id=$DISCO_ID"
RELAY="relay://$BRIDGE_IP:22067/?id=$RELAY_ID"
echo "discovery $DISCOVERY"
echo "relay     $RELAY"

# ---------------------------------------------------------------------------------
log "the VM: a LUKS2 root and dracut, with nothing of synctang on it yet"
rm -f "$RUN/id" "$RUN/id.pub"
ssh-keygen -q -t ed25519 -N '' -f "$RUN/id"
cp "$RUN/id.pub" "$VMDIR/authorized_keys"
if [ -f "$WORK/cache/rootfs.tar.xz" ]; then
	cp "$WORK/cache/rootfs.tar.xz" "$VMDIR/rootfs.tar.xz"
fi
OUTDIR="$VMDIR" VM_PASSPHRASE="$VM_PASSPHRASE" "$REPO/scripts/make-test-vm.sh" > "$LOGS/make-test-vm.log" 2>&1 \
	|| { tail -20 "$LOGS/make-test-vm.log" >&2; fail "could not build the VM image (see $LOGS/make-test-vm.log)"; }
[ -f "$WORK/cache/rootfs.tar.xz" ] || cp "$VMDIR/rootfs.tar.xz" "$WORK/cache/rootfs.tar.xz"

# The first interface is on the private bridge, where the machine finds the relay and
# the discovery server; the second is QEMU's own, only so that this script can ssh in and
# apt can fetch what the packages need.
start_vm() {
	OUTDIR="$VMDIR" NET=tap CONSOLE_WAIT=1 \
		MONITOR_SOCK="$VMDIR/monitor.sock" \
		"$REPO/scripts/run-test-vm.sh" \
		-netdev "user,id=net1,hostfwd=tcp:127.0.0.1:$SSH_PORT-:22" \
		-device "virtio-net-pci,netdev=net1,mac=52:54:00:12:34:57" \
		-pidfile "$VMDIR/qemu.pid" > "$LOGS/qemu.log" 2>&1 &
	PIDS+=("$!")
	python3 "$REPO/scripts/e2e-console.py" "$CONSOLE_PORT" "$CONSOLE_LOG" "$FIFO" > "$LOGS/console-helper.log" 2>&1 &
	PIDS+=("$!")
}

log_size() { stat -c %s "$CONSOLE_LOG"; }

# wait_console PATTERN FROM-OFFSET SECONDS: the console printed PATTERN after FROM-OFFSET.
wait_console() {
	local pattern=$1 from=$2 seconds=$3 i
	for ((i = 0; i < seconds; i++)); do
		if tail -c +"$((from + 1))" "$CONSOLE_LOG" | grep -aiq -- "$pattern"; then
			return 0
		fi
		sleep 1
	done
	return 1
}

vm() {
	ssh -i "$RUN/id" -p "$SSH_PORT" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
		-o LogLevel=ERROR -o ConnectTimeout=5 -o BatchMode=yes ubuntu@127.0.0.1 "$@"
}

wait_ssh() {
	local i
	for ((i = 0; i < ${1:-180}; i++)); do
		if vm true 2> /dev/null; then return 0; fi
		sleep 2
	done
	return 1
}

start_vm
START=$(log_size)
wait_console "enter passphrase" "$START" 240 || fail "the VM never asked for a passphrase (see $CONSOLE_LOG)"
sleep 1
echo "$VM_PASSPHRASE" > "$FIFO"
wait_ssh || fail "the VM did not come up after the passphrase (see $CONSOLE_LOG)"
echo "the VM is up"

# ---------------------------------------------------------------------------------
log "the packages"
scp -i "$RUN/id" -P "$SSH_PORT" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR \
	"$UNLOCKER_DEB" "$DRACUT_DEB" ubuntu@127.0.0.1:/tmp/ > /dev/null
vm "sudo DEBIAN_FRONTEND=noninteractive apt-get install -y /tmp/$(basename "$UNLOCKER_DEB") /tmp/$(basename "$DRACUT_DEB")" \
	> "$LOGS/apt.log" 2>&1 || { tail -20 "$LOGS/apt.log" >&2; fail "could not install the packages (see $LOGS/apt.log)"; }
check "the machine program is installed" vm 'command -v synctang-unlocker > /dev/null'
check "its manual page is installed" vm 'man -w synctang-unlocker > /dev/null'
check "its shell completion is installed" vm 'test -s /usr/share/bash-completion/completions/synctang-unlocker && test -s /usr/share/zsh/vendor-completions/_synctang-unlocker && test -s /usr/share/fish/vendor_completions.d/synctang-unlocker.fish'
check "bash can load it and offers the commands" vm "bash -c 'source /usr/share/bash-completion/completions/synctang-unlocker; COMP_WORDS=(synctang-unlocker dev); COMP_CWORD=1; _synctang_unlocker; [ \"\${COMPREPLY[0]}\" = devices ]'"

mkdir -p "$RUN/bin/keyholder"
dpkg-deb -x "$KEYHOLDER_DEB" "$RUN/bin/keyholder"
KEYHOLDER="$RUN/bin/keyholder/usr/bin/synctang-keyholder"
E2EPHONE="$RUN/bin/e2ephone"
(cd "$REPO" && go build -o "$E2EPHONE" ./integration/e2ephone)

vm "sudo install -d -m 0755 /etc/synctang && printf 'relay-pool=%s\ndiscovery=%s\n' '$RELAY' '$DISCOVERY' | sudo tee /etc/synctang/network.conf > /dev/null"

# ---------------------------------------------------------------------------------
log "two key holders, one enrolled from the laptop, one paired from the phone, and a stranger"
LAPTOP="$RUN/laptop"
STRANGER="$RUN/stranger"
PHONE="$RUN/phone"
mkdir -p "$LAPTOP" "$STRANGER"
# Fresh keys every run: init refuses to overwrite one, rightly.
rm -f "$LAPTOP"/synctang-keyholder/key "$LAPTOP"/synctang-keyholder/transport.pem \
	"$STRANGER"/synctang-keyholder/key "$STRANGER"/synctang-keyholder/transport.pem
XDG_CONFIG_HOME="$LAPTOP" "$KEYHOLDER" init > "$LOGS/laptop-init.log" 2>&1
XDG_CONFIG_HOME="$STRANGER" "$KEYHOLDER" init > "$LOGS/stranger-init.log" 2>&1
LAPTOP_INFO="$(XDG_CONFIG_HOME="$LAPTOP" "$KEYHOLDER" export-pubkey)"
LAPTOP_PUB="$(sed -n 's/^S: //p' <<< "$LAPTOP_INFO")"
LAPTOP_ID="$(sed -n 's/^transport-id: //p' <<< "$LAPTOP_INFO")"
[ -n "$LAPTOP_PUB" ] && [ -n "$LAPTOP_ID" ] || fail "could not read the laptop key holder's key"

vm "printf %s '$VM_PASSPHRASE' | sudo synctang-unlocker enrol --device /dev/vda3 --name e2e-vm \
	--pubkey '$LAPTOP_PUB' --recipient-transport-id '$LAPTOP_ID' --existing-passphrase-file -" \
	> "$LOGS/enrol.log" 2>&1 || { cat "$LOGS/enrol.log" >&2; fail "enrol failed"; }
STATUS_BEFORE="$(vm 'sudo synctang-unlocker status --device /dev/vda3')"
MACHINE_ID="$(sed -n "s/^this machine's transport id: //p" <<< "$STATUS_BEFORE" | head -1)"
[ -n "$MACHINE_ID" ] || fail "no machine transport id"
echo "machine $MACHINE_ID"

# The initrd was built when the packages went in, before this machine had an identity, so it
# cannot carry it: enrol and status must both say so.
check "enrol warns that the initrd lacks the new identity" grep -q "does not contain this machine's identity" "$LOGS/enrol.log"
check "enrol names the fix" grep -q "dracut --force" "$LOGS/enrol.log"
check "status says the initrd lacks the identity" grep -q "initrd: does not contain this machine's identity" <<< "$STATUS_BEFORE"

# The phone pairs from the link the machine shows, as the app does after a scan.
"$E2EPHONE" init "$PHONE" > "$LOGS/phone-init.log"
vm "sudo bash -c \"nohup synctang-unlocker pair --device /dev/vda3 --name e2e-vm --yes --timeout 5m --rebuild-initrd \
	--existing-passphrase-file <(printf %s '$VM_PASSPHRASE') > /tmp/pair.out 2>&1 &\""
LINK=""
for _ in $(seq 1 60); do
	LINK="$(vm "grep -a -o 'synctang://pair[^ ]*' /tmp/pair.out" 2> /dev/null | head -1 || true)"
	[ -n "$LINK" ] && break
	sleep 1
done
[ -n "$LINK" ] || { vm 'cat /tmp/pair.out' >&2 || true; fail "the machine showed no pairing link"; }
"$E2EPHONE" pair "$PHONE" "$LINK" > "$LOGS/phone-pair.log" 2>&1 || { cat "$LOGS/phone-pair.log" >&2; fail "the phone could not pair"; }
for _ in $(seq 1 30); do
	vm 'grep -aq "paired" /tmp/pair.out' && break
	sleep 1
done

# pair rebuilds the initrd after the pairing, because it was asked to and it was wrong; wait
# for it to finish.
for _ in $(seq 1 180); do
	vm 'pgrep -f "[s]ynctang-unlocker pair" > /dev/null' || break
	sleep 2
done

STATUS="$(vm 'sudo synctang-unlocker status --device /dev/vda3')"
check "both key holders are enrolled" test "$(grep -c 'keyslot' <<< "$STATUS")" -eq 2

# ---------------------------------------------------------------------------------
log "the initrd carries the identity and the network settings"
check "pair said it rebuilt the initrd" vm 'grep -aq "rebuilding the initrd" /tmp/pair.out'
check "status now says the initrd carries the identity" grep -q "initrd: carries this machine's identity" <<< "$STATUS"
INITRD="$(vm 'sudo lsinitrd')"
check "the machine identity is in the initrd" grep -q 'etc/synctang/machine.pem' <<< "$INITRD"
check "network.conf is in the initrd" grep -q 'etc/synctang/network.conf' <<< "$INITRD"
check "the agent is in the initrd" grep -q 'usr/local/bin/synctang-unlocker' <<< "$INITRD"

# ---------------------------------------------------------------------------------
log "a rehearsal of the unlock, on the running machine"
if vm 'sudo synctang-unlocker rehearse --initrd-only' > "$LOGS/rehearse-initrd.log" 2>&1; then
	echo "ok   rehearse finds nothing wrong with the initrd"
else
	echo "FAIL rehearse finds a problem with the initrd"
	cat "$LOGS/rehearse-initrd.log"
	FAILED=1
fi
vm "sudo bash -c 'nohup synctang-unlocker rehearse --timeout 4m > /tmp/rehearse.out 2>&1 &'"
for _ in $(seq 1 60); do
	vm 'grep -aq "waiting for a key holder" /tmp/rehearse.out' && break
	sleep 1
done
XDG_CONFIG_HOME="$LAPTOP" timeout 250 "$KEYHOLDER" unlock --yes --timeout 4m --discovery "$DISCOVERY" "$MACHINE_ID" \
	> "$LOGS/rehearse-unlock.log" 2>&1 || { cat "$LOGS/rehearse-unlock.log" >&2; FAILED=1; }
for _ in $(seq 1 30); do
	vm 'pgrep -f "[s]ynctang-unlocker rehearse" > /dev/null' || break
	sleep 1
done
check "the rehearsal passed" vm 'grep -aq "rehearsal passed" /tmp/rehearse.out'
check "the rehearsal said what it could not test" vm 'grep -aq "not tested" /tmp/rehearse.out'
# shellcheck disable=SC2016  # runs in the VM, where the substitution is wanted
check "the rehearsal opened nothing" vm '[ "$(ls /dev/mapper | grep -c synctang-)" -eq 0 ]'

# ---------------------------------------------------------------------------------
# reboot_to_prompt: restarts the VM and waits until it is asking for the passphrase.
reboot_to_prompt() {
	MARK=$(log_size)
	vm 'sudo systemctl reboot' > /dev/null 2>&1 || true
	wait_console "enter passphrase" "$MARK" 240 || fail "the VM did not come back to its passphrase prompt (see $CONSOLE_LOG)"
}

# shellcheck disable=SC2329  # run by check
unlocked_without_a_passphrase() {
	wait_ssh 120 && vm 'sudo journalctl -b -t synctang-unlocker --no-pager' | grep -q 'recovery attempt succeeded'
}

log "reboot; a stranger, then the laptop"
reboot_to_prompt
if XDG_CONFIG_HOME="$STRANGER" timeout 120 "$KEYHOLDER" unlock --yes --timeout 40s --discovery "$DISCOVERY" "$MACHINE_ID" \
	> "$LOGS/stranger-unlock.log" 2>&1; then
	printf 'FAIL a key holder that was never enrolled unlocked the machine\n'
	FAILED=1
else
	printf 'ok   a key holder that was never enrolled got nowhere\n'
fi
check "the machine is still at its prompt after the stranger" bash -c "! tail -c +$((MARK + 1)) '$CONSOLE_LOG' | grep -aq 'login:'"
XDG_CONFIG_HOME="$LAPTOP" timeout 300 "$KEYHOLDER" unlock --yes --timeout 3m --discovery "$DISCOVERY" "$MACHINE_ID" \
	> "$LOGS/laptop-unlock.log" 2>&1 || { cat "$LOGS/laptop-unlock.log" >&2; FAILED=1; }
check "the laptop unlocked the machine" unlocked_without_a_passphrase
check "nobody typed a passphrase on the console" bash -c "! tail -c +$((MARK + 1)) '$CONSOLE_LOG' | grep -aq '$VM_PASSPHRASE'"

log "reboot; the phone"
reboot_to_prompt
timeout 300 "$E2EPHONE" unlock "$PHONE" "$MACHINE_ID" "$DISCOVERY" > "$LOGS/phone-unlock.log" 2>&1 \
	|| { cat "$LOGS/phone-unlock.log" >&2; FAILED=1; }
check "the phone unlocked the machine" unlocked_without_a_passphrase
check "nobody typed a passphrase on the console either" bash -c "! tail -c +$((MARK + 1)) '$CONSOLE_LOG' | grep -aq '$VM_PASSPHRASE'"

if [ "$FAILED" -eq 0 ]; then
	log "end to end: all checks passed"
else
	log "end to end: FAILED (logs in $LOGS)"
fi
exit "$FAILED"
