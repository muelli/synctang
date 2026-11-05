// SPDX-License-Identifier: AGPL-3.0-or-later

package gnuflag

import (
	"bytes"
	"flag"
	"strings"
	"testing"
	"time"
)

func newSet(out *bytes.Buffer) (*flag.FlagSet, *string, *time.Duration) {
	fs := flag.NewFlagSet("enrol", flag.ContinueOnError)
	fs.SetOutput(Writer(out))
	name := fs.String("name", "", "this machine's name, shown to the key holder (default: the name given at enrolment)")
	timeout := fs.Duration("timeout", time.Minute, "give up after this long")
	fs.Bool("yes", false, "answer yes; also spelled -y in other tools")
	return fs, name, timeout
}

func TestHelpSpellsEveryOptionWithTwoDashes(t *testing.T) {
	var out bytes.Buffer
	fs, _, _ := newSet(&out)
	fs.PrintDefaults()
	for _, want := range []string{"  --name string\n", "  --timeout duration\n", "  --yes\n"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "\n  -name") || strings.HasPrefix(out.String(), "  -name") {
		t.Errorf("a single-dash option is left:\n%s", out.String())
	}
}

func TestHelpTextAfterTheOptionIsNotTouched(t *testing.T) {
	var out bytes.Buffer
	fs, _, _ := newSet(&out)
	fs.PrintDefaults()
	if !strings.Contains(out.String(), "also spelled -y in other tools") {
		t.Errorf("the usage text was altered:\n%s", out.String())
	}
}

func TestErrorsNameTheOptionTheGNUWay(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--device", "x"}, "unrecognised option '--device'"},
		{[]string{"-device", "x"}, "unrecognised option '--device'"},
		{[]string{"--name"}, "option '--name' requires an argument"},
		{[]string{"--timeout=soon"}, "invalid value \"soon\" for option '--timeout'"},
	} {
		var out bytes.Buffer
		fs, _, _ := newSet(&out)
		if err := fs.Parse(tc.args); err == nil {
			t.Fatalf("%v: no error", tc.args)
		}
		if !strings.Contains(out.String(), tc.want) {
			t.Errorf("%v: output lacks %q:\n%s", tc.args, tc.want, out.String())
		}
		if strings.Contains(out.String(), "flag provided") || strings.Contains(out.String(), "flag needs") {
			t.Errorf("%v: Go's own wording is left:\n%s", tc.args, out.String())
		}
	}
}

func TestBothSpellingsStillParse(t *testing.T) {
	for _, args := range [][]string{{"--name", "box"}, {"-name", "box"}, {"--name=box"}} {
		var out bytes.Buffer
		fs, name, _ := newSet(&out)
		if err := fs.Parse(args); err != nil || *name != "box" {
			t.Errorf("%v: %v, name %q", args, err, *name)
		}
	}
}

func TestWriterReportsTheLengthItWasGiven(t *testing.T) {
	var out bytes.Buffer
	in := []byte("  -name string\n")
	n, err := Writer(&out).Write(in)
	if err != nil || n != len(in) {
		t.Fatalf("n=%d err=%v, want %d", n, err, len(in))
	}
}
