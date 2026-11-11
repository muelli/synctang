// SPDX-License-Identifier: AGPL-3.0-or-later

package gnuflag

import (
	"testing"

	"github.com/muelli/synctang/internal/i18n/markcheck"
)

// The messages this package gives a person are marked with i18n.T, so that they can be collected for translation.
func TestEveryMessageShownToAPersonIsMarked(t *testing.T) {
	units, err := markcheck.Find(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range units {
		t.Errorf("not marked with i18n.T: %s", u)
	}
	problems, err := markcheck.Dynamic(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}
