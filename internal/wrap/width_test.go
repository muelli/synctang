// SPDX-License-Identifier: AGPL-3.0-or-later

package wrap

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestWidthOfAnythingThatIsNotATerminalIsTheDefault(t *testing.T) {
	var buf bytes.Buffer
	if got := Width(&buf); got != DefaultWidth {
		t.Errorf("a buffer: %d", got)
	}
	f, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if got := Width(f); got != DefaultWidth {
		t.Errorf("a file: %d", got)
	}
}

func TestClampKeepsLinesReadable(t *testing.T) {
	for _, tc := range []struct{ columns, want int }{
		{80, 78}, {120, 100}, {200, 100}, {30, 40}, {1, 40}, {0, DefaultWidth}, {-5, DefaultWidth},
	} {
		if got := forColumns(tc.columns); got != tc.want {
			t.Errorf("%d columns: %d, want %d", tc.columns, got, tc.want)
		}
	}
}
