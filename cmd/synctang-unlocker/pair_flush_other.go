// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux

package main

import (
	"bufio"
	"io"
)

// dropTypedAhead is only done on Linux, which is the only system this program unlocks anything on.
func dropTypedAhead(*bufio.Reader, io.Reader) {}
