// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"testing"

	"github.com/muelli/synctang/internal/i18n/markcheck"
)

// Every message that is shown to a person is marked with T, so that it can be collected for translation. A new
// message that is printed without the mark fails here; see package markcheck for what counts as a message.
func TestEveryMessageShownToAPersonIsMarked(t *testing.T) {
	units, err := markcheck.Find(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range units {
		t.Errorf("not marked with T: %s", u)
	}
}

// A mark is only useful if the message can be collected from the source: T is called with a string constant.
func TestEveryMarkIsAStringConstant(t *testing.T) {
	problems, err := markcheck.Dynamic(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}
