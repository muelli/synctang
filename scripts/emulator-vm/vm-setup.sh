#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Sets up a fresh Ubuntu 26.04 VM as the emulator host. Run as root, once,
# from the first-boot bootstrap that emu-ctl.sh writes; the log is
# /var/log/emulator-setup.log. When it has finished, /var/lib/emulator-vm-ready
# exists.
#
#   IDLE_MINUTES=60 MAX_RUNTIME_HOURS=10 scripts/emulator-vm/vm-setup.sh
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)
user=${EMU_USER:-ubuntu}
: "${IDLE_MINUTES:=60}" "${MAX_RUNTIME_HOURS:=10}"

export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
# crun as well as podman: rootless containers need a runtime, and crun is
# the one that supports the options documented in the Containerfile.
apt-get install -y -qq podman crun git android-tools-adb

# The emulator needs read-write access to /dev/kvm, and a rootless
# container does not inherit supplementary groups under every runtime. This
# is a single-user build machine, so make the node world accessible, as
# GitHub's hosted runners do.
echo 'KERNEL=="kvm", GROUP="kvm", MODE="0666", OPTIONS+="static_node=kvm"' > /etc/udev/rules.d/99-kvm4all.rules
udevadm control --reload-rules
udevadm trigger --name-match=kvm || true
usermod -aG kvm "$user"
# Rootless podman keeps running for the user without a login session.
loginctl enable-linger "$user"

# Shut off when idle. Set up before the long image build, so a setup that
# fails half way still cannot leave the instance running for ever.
install -m 0755 "$here/idle-shutdown.sh" /usr/local/bin/emulator-idle-shutdown
cat > /etc/emulator-vm.conf <<EOF
IDLE_MINUTES=$IDLE_MINUTES
MAX_RUNTIME_HOURS=$MAX_RUNTIME_HOURS
EOF
cat > /etc/systemd/system/emulator-idle.service <<'EOF'
[Unit]
Description=Power the emulator VM off when it has been idle

[Service]
Type=oneshot
ExecStart=/usr/local/bin/emulator-idle-shutdown
EOF
cat > /etc/systemd/system/emulator-idle.timer <<'EOF'
[Unit]
Description=Check whether the emulator VM is idle

[Timer]
OnBootSec=5min
OnUnitActiveSec=5min

[Install]
WantedBy=timers.target
EOF
systemctl daemon-reload
systemctl enable --now emulator-idle.timer

chown -R "$user:$user" "$repo"
runuser -l "$user" -c "cd '$repo' && podman build -t synctang-emulator -f scripts/emulator-vm/Containerfile scripts/emulator-vm"

# A full idle period starts now, not at boot: the build above can take a
# good part of the first one.
mkdir -p /run/emulator-vm
touch /run/emulator-vm/keepalive
touch /var/lib/emulator-vm-ready
echo "emulator VM ready"
