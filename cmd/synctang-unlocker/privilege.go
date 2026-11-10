// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var (
	lookPath    = exec.LookPath
	commandLine = func() []string { return os.Args }
)

// asRoot is the command that was just run, to run as root: with run0 where the system has it (systemd 256 and
// later, which asks for authorisation in the terminal), and with sudo where it does not. The program is named
// as a person types it, without the path.
func asRoot() string {
	tool := "sudo"
	if _, err := lookPath("run0"); err == nil {
		tool = "run0"
	}
	args := commandLine()
	parts := []string{tool, filepath.Base(args[0])}
	for _, a := range args[1:] {
		if a == "" || strings.ContainsAny(a, " \t\n'\"$&;|<>()*?[]{}\\`!#~") {
			a = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

// rootHint is what to add to a message about something that takes root: how to run the command as root.
func rootHint() string { return "run it as root: " + asRoot() }

// noDeviceError is the error for a search of the block devices that found no mr-1 token. Reading a LUKS header
// takes root, and a search made as an ordinary user fails to read every one of them: that is not "no device
// has a token", and the message says what was wrong.
func noDeviceError(root bool) error {
	if !root {
		return fmt.Errorf("no block device could be checked for an %s token: reading a LUKS header takes root; %s", mrTokenType, rootHint())
	}
	return fmt.Errorf("no block device carries an %s token", mrTokenType)
}

// permissionHint is what to add to the message of a failed read of a LUKS header when the reason was
// permission and the program was not run as root: how to run it as root. Nothing for any other failure.
func permissionHint(err error) string {
	if err == nil || isRoot() {
		return ""
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "permission") || strings.Contains(text, "not permitted") || strings.Contains(text, "eacces") {
		return "; reading a LUKS header takes root, " + rootHint()
	}
	return ""
}
