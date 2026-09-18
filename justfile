# SPDX-License-Identifier: AGPL-3.0-or-later

# Run everything CI runs, except the privileged loopback-LUKS tests and Android.
test: test-go lint em-dash-check

test-go:
	go test ./...

# Loopback LUKS tests exercise real dm-crypt via a truncated file image.
# They need CAP_SYS_ADMIN, so they run in a separate privileged job (or on the VM).
test-go-privileged:
	go test -tags privileged ./...

lint:
	find dracut scripts -name '*.sh' -print0 | xargs -0 -r shellcheck

em-dash-check:
	./scripts/check-em-dash.sh

install-dracut:
	sudo install -d /usr/lib/dracut/modules.d/90unlocker
	sudo install -m 0755 dracut/90unlocker/module-setup.sh /usr/lib/dracut/modules.d/90unlocker/
	sudo install -m 0755 dracut/90unlocker/unlocker-start.sh /usr/lib/dracut/modules.d/90unlocker/
