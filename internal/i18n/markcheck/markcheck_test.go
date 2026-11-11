// SPDX-License-Identifier: AGPL-3.0-or-later

package markcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// found runs Find on one file of source and returns the messages it found, as text.
func found(t *testing.T, body string) []Unit {
	t.Helper()
	dir := t.TempDir()
	src := "package main\n\n" + body
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	units, err := Find(dir)
	if err != nil {
		t.Fatalf("%v\n%s", err, src)
	}
	return units
}

func texts(units []Unit) []string {
	var out []string
	for _, u := range units {
		out = append(out, u.Text)
	}
	return out
}

func TestWhatIsPassedToThePrintingFunctionsIsAMessage(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want []string
	}{
		"ui.Errorf format":            {`func f() { ui.Errorf(stderr, "bad thing: %v\n", err) }`, []string{"bad thing: %v\n"}},
		"ui.Notef":                    {`func f() { ui.Notef(stderr, "still waiting\n") }`, []string{"still waiting\n"}},
		"ui.Errorln":                  {`func f() { ui.Errorln(stderr, "no device") }`, []string{"no device"}},
		"ui.Successln":                {`func f() { ui.Successln(stdout, "done") }`, []string{"done"}},
		"fmt.Fprintf format only":     {`func f() { fmt.Fprintf(stdout, "paired %s\n", name) }`, []string{"paired %s\n"}},
		"fmt.Fprintln every literal":  {`func f() { fmt.Fprintln(stdout, "one", name, "two") }`, []string{"one", "two"}},
		"fmt.Fprint":                  {`func f() { fmt.Fprint(stderr, "plain text") }`, []string{"plain text"}},
		"fmt.Errorf":                  {`func f() error { return fmt.Errorf("reading %s: %w", p, err) }`, []string{"reading %s: %w"}},
		"errors.New":                  {`func f() error { return errors.New("boom") }`, []string{"boom"}},
		"a flag's description":        {`func f() { fs.String("name", "", "the name to use") }`, []string{"the name to use"}},
		"a flag, bool":                {`func f() { fs.Bool("yes", false, "say yes") }`, []string{"say yes"}},
		"a table's headers":           {`func f() { ui.Table([]string{"TRANSPORT ID", "KEY"}, rows, look) }`, []string{"TRANSPORT ID", "KEY"}},
		"a line's text":               {`func f() { look.Line(look.OK(""), "paired") }`, []string{"paired"}},
		"literals joined with +":      {`func f() { fmt.Fprintf(stdout, "first line\n"+"second line %s\n", x) }`, []string{"first line\nsecond line %s\n"}},
		"a literal in a longer chain": {`func f() { fmt.Fprintln(stdout, name+" has enrolled this key holder") }`, []string{" has enrolled this key holder"}},
	} {
		t.Run(name, func(t *testing.T) {
			got := texts(found(t, tc.body))
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWhatIsAlreadyMarkedIsNotReported(t *testing.T) {
	body := `func f() {
	ui.Errorf(stderr, T("bad thing: %v\n"), err)
	fmt.Fprintln(stdout, T("one"), name, T("two"))
	fs.String("name", "", T("the name"))
	_ = errors.New(T("boom"))
	fmt.Fprintf(stdout, T("a "+"b %s\n"), x)
	_ = i18n.T("a message with several words")
	return fmt.Errorf(i18n.T("reading %s: %w"), p, err)
}`
	if got := found(t, body); len(got) != 0 {
		t.Errorf("reported %v", got)
	}
}

func TestWhatIsNotText(t *testing.T) {
	for name, body := range map[string]string{
		"a format with no words":     `func f() { fmt.Fprintf(stdout, "%s: %d\n", a, b) }`,
		"a separator":                `func f() { fmt.Fprintln(stdout, "--", x) }`,
		"a value after the format":   `func f() { fmt.Fprintf(stdout, "%s\n", "value") }`,
		"a string that is not shown": `func f() { m["key name"] = x; os.Setenv("SOME_KEY", "value") }`,
		"Sprintf":                    `func f() string { return fmt.Sprintf("looking for %s", x) }`,
		"a debug line":               `func f() { ui.Debugf(stderr, "reading the file %s\n", p) }`,
		"a flag's name and default":  `func f() { fs.String("name", "default words", T("the name")) }`,
	} {
		t.Run(name, func(t *testing.T) {
			if got := found(t, body); len(got) != 0 {
				t.Errorf("reported %v", got)
			}
		})
	}
}

func TestAPrintfWithNothingToFormatIsSaidSo(t *testing.T) {
	got := found(t, `func f() {
	fmt.Fprintf(stdout, "just words\n")
	fmt.Fprintf(stdout, "words %s\n", x)
}`)
	if len(got) != 2 || !got[0].ZeroArg || got[1].ZeroArg {
		t.Errorf("got %v", got)
	}
}

func TestTestFilesAreNotLookedAt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x_test.go"), []byte("package main\n\nfunc f() { fmt.Fprintln(stdout, \"words\") }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	units, err := Find(dir)
	if err != nil || len(units) != 0 {
		t.Errorf("%v %v", units, err)
	}
}

func TestOffsetsCoverExactlyTheMessage(t *testing.T) {
	dir := t.TempDir()
	src := "package main\n\nfunc f() { fmt.Fprintf(stdout, \"ab \"+\n\t\"cd\\n\", x) }\n"
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	units, err := Find(dir)
	if err != nil || len(units) != 1 {
		t.Fatalf("%v %v", units, err)
	}
	if got := src[units[0].Start:units[0].End]; got != "\"ab \"+\n\t\"cd\\n\"" {
		t.Errorf("covered %q", got)
	}
}

func TestAMarkWithSomethingThatIsNotAConstantIsReported(t *testing.T) {
	dir := t.TempDir()
	src := "package main\n\nfunc f() { _ = T(\"fine\"); _ = T(\"fine \" + \"too\"); _ = T(name); _ = i18n.T(fmt.Sprint(1)) }\n"
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Dynamic(dir)
	if err != nil || len(got) != 2 {
		t.Errorf("%v %v", got, err)
	}
}
