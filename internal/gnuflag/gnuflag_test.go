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

// A description longer than a terminal is wide must wrap under its option, not back to the left margin,
// where it reads as if it were another option.
func TestLongHelpWrapsUnderTheOption(t *testing.T) {
	var out bytes.Buffer
	fs := flag.NewFlagSet("pair", flag.ContinueOnError)
	fs.SetOutput(Writer(&out))
	fs.String("store", "auto", "if there is no key yet, where to keep the one made: auto, file, secret-service, systemd-creds, systemd-creds:tpm2")
	fs.Duration("wait", 5*time.Minute, "how long to wait")
	fs.PrintDefaults()
	var continuation int
	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if len(line) > 78 {
			t.Errorf("a line of %d columns:\n%s", len(line), out.String())
		}
		if strings.HasPrefix(line, "      ") {
			continuation++
		} else if !strings.HasPrefix(line, "  --") {
			t.Errorf("a line at neither the option's nor the description's indent: %q\n%s", line, out.String())
		}
	}
	if continuation < 3 {
		t.Errorf("the description was not wrapped into lines of its own:\n%s", out.String())
	}
	// Nothing is lost by wrapping.
	if got := strings.Join(strings.Fields(out.String()), " "); !strings.Contains(got, "where to keep the one made: auto, file, secret-service, systemd-creds, systemd-creds:tpm2 (default \"auto\")") {
		t.Errorf("words were lost or reordered:\n%s", out.String())
	}
}

func TestShortHelpIsStillOneLineUnderItsOption(t *testing.T) {
	var out bytes.Buffer
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	fs.SetOutput(Writer(&out))
	fs.Bool("yes", false, "answer yes")
	fs.PrintDefaults()
	if want := "  --yes\n      answer yes\n"; out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
}
