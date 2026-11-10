// SPDX-License-Identifier: AGPL-3.0-or-later

package completion

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const sampleUsage = `usage: synctang-unlocker <command> [flags]

commands:
  enrol   add a key holder's public key to a LUKS volume (or --remove
          or --replace one that is already enrolled)
  agent   run the systemd password agent, answering recovery attempts
  devices list the LUKS devices on this machine and what is enrolled on each
  open    wait for a key holder, then open a LUKS volume
`

const sampleHelp = `Usage of enrol:
  -device string
    	LUKS2 device or image to enrol
  -existing-passphrase-file string
    	file containing an existing valid LUKS passphrase (- for stdin)
  -timeout duration
    	give up after this long (default 2m0s)
  -yes
    	skip the approval prompt
`

func TestParseUsageReadsTheCommandsAndJoinsTheirSummaries(t *testing.T) {
	got := ParseUsage(sampleUsage)
	var names []string
	for _, c := range got {
		names = append(names, c.Name)
	}
	if strings.Join(names, " ") != "enrol agent devices open" {
		t.Fatalf("commands = %v", names)
	}
	want := "add a key holder's public key to a LUKS volume (or --remove or --replace one that is already enrolled)"
	if got[0].Summary != want {
		t.Fatalf("enrol summary = %q, want %q", got[0].Summary, want)
	}
	if got[2].Summary != "list the LUKS devices on this machine and what is enrolled on each" {
		t.Fatalf("devices summary = %q (a name as long as the column leaves one space)", got[2].Summary)
	}
}

func TestParseHelpReadsFlagsTheirValuesAndTheirUsage(t *testing.T) {
	got := ParseHelp(sampleHelp)
	if len(got) != 4 {
		t.Fatalf("flags = %+v", got)
	}
	if got[0] != (Flag{Name: "device", Value: "string", Usage: "LUKS2 device or image to enrol"}) {
		t.Errorf("device = %+v", got[0])
	}
	// Go appends "(default ...)" to a flag's usage; for a path it is the home directory of whoever
	// ran the program, which has no place in a script that is written once and installed on
	// other machines. The description is what the person wrote.
	if got[2] != (Flag{Name: "timeout", Value: "duration", Usage: "give up after this long"}) {
		t.Errorf("timeout = %+v", got[2])
	}
	if got[3] != (Flag{Name: "yes", Usage: "skip the approval prompt"}) {
		t.Errorf("a flag with no value must have none: %+v", got[3])
	}
	if len(ParseHelp("Usage of devices:\n")) != 0 {
		t.Error("a command with no flags must give none")
	}
}

func TestBuildAsksEveryCommandForItsHelp(t *testing.T) {
	var asked []string
	p := Build("synctang-unlocker", sampleUsage, func(cmd string) string {
		asked = append(asked, cmd)
		if cmd == "enrol" {
			return sampleHelp
		}
		return "Usage of " + cmd + ":\n"
	})
	if strings.Join(asked, " ") != "enrol agent devices open" {
		t.Fatalf("asked %v", asked)
	}
	if p.Name != "synctang-unlocker" || len(p.Commands) != 4 || len(p.Commands[0].Flags) != 4 {
		t.Fatalf("%+v", p)
	}
}

func TestWhichFlagsTakeAFile(t *testing.T) {
	for name, want := range map[string]bool{
		"device": true, "existing-passphrase-file": true, "machine-key-file": true,
		"key-file": true, "network-conf": true, "config": true, "name": false,
		"timeout": false, "pubkey": false, "discovery": false, "qr": false,
	} {
		if got := (Flag{Name: name, Value: "string"}).TakesFile(); got != want {
			t.Errorf("%s: TakesFile = %v, want %v", name, got, want)
		}
	}
	if (Flag{Name: "yes"}).TakesFile() {
		t.Error("a flag with no value takes no file")
	}
}

var sample = Program{Name: "synctang-unlocker", Commands: []Command{
	{Name: "enrol", Summary: "add a key holder", Flags: []Flag{
		{Name: "device", Value: "string", Usage: "LUKS2 device or image"},
		{Name: "existing-passphrase-file", Value: "string", Usage: "file with a passphrase"},
		{Name: "name", Value: "string", Usage: "machine name"},
		{Name: "yes", Usage: "skip the question"},
	}},
	{Name: "status", Summary: "show who is enrolled", Flags: []Flag{{Name: "device", Value: "string", Usage: "device"}}},
	{Name: "open", Summary: "open a volume", PositionalFiles: true},
	{Name: "completion", Summary: "print a shell completion script", Positional: []string{"bash", "zsh", "fish"}},
}}

func lookPath(t *testing.T, shell string) string {
	t.Helper()
	p, err := exec.LookPath(shell)
	if err != nil {
		t.Skipf("%s is not installed", shell)
	}
	return p
}

// bashComplete sources the generated script in dir and returns what completing words, with
// the cursor on the last one, offers.
func bashComplete(t *testing.T, dir string, words ...string) []string {
	t.Helper()
	bash := lookPath(t, "bash")
	script, err := Generate("bash", sample)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "completion.bash")
	if err := os.WriteFile(file, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = "'" + w + "'"
	}
	// COMPREPLY starts out holding an answer from "an earlier call": a script that returns
	// without answering must not leave that standing.
	harness := `source "$1"; COMPREPLY=(stale); COMP_WORDS=(` + strings.Join(quoted, " ") + `); COMP_CWORD=` +
		string(rune('0'+len(words)-1)) + `; _synctang_unlocker; printf '%s\n' "${COMPREPLY[@]}"`
	cmd := exec.Command(bash, "-c", harness, "bash", file)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash: %v\n%s", err, out)
	}
	var got []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			got = append(got, l)
		}
	}
	sort.Strings(got)
	return got
}

func TestBashOffersTheCommandsFirst(t *testing.T) {
	got := bashComplete(t, t.TempDir(), "synctang-unlocker", "")
	if strings.Join(got, " ") != "completion enrol open status" {
		t.Fatalf("got %v", got)
	}
	if got := bashComplete(t, t.TempDir(), "synctang-unlocker", "en"); strings.Join(got, " ") != "enrol" {
		t.Fatalf("got %v", got)
	}
}

func TestBashOffersAcommandsFlagsAsTheyAreTyped(t *testing.T) {
	got := bashComplete(t, t.TempDir(), "synctang-unlocker", "enrol", "--")
	if strings.Join(got, " ") != "--device --existing-passphrase-file --name --yes" {
		t.Fatalf("got %v", got)
	}
	if got := bashComplete(t, t.TempDir(), "synctang-unlocker", "enrol", "--de"); strings.Join(got, " ") != "--device" {
		t.Fatalf("got %v", got)
	}
	if got := bashComplete(t, t.TempDir(), "synctang-unlocker", "status", "--"); strings.Join(got, " ") != "--device" {
		t.Fatalf("each command has its own flags: %v", got)
	}
	if got := bashComplete(t, t.TempDir(), "synctang-unlocker", "nonsense", "--"); len(got) != 0 {
		t.Fatalf("an unknown command offers nothing: %v", got)
	}
}

func TestBashCompletesAFileAfterAFileFlagAndNothingAfterOtherValues(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := bashComplete(t, dir, "synctang-unlocker", "enrol", "--existing-passphrase-file", "sec"); strings.Join(got, " ") != "secret.txt" {
		t.Fatalf("after a file flag: %v", got)
	}
	if got := bashComplete(t, dir, "synctang-unlocker", "enrol", "--name", ""); len(got) != 0 {
		t.Fatalf("a value that cannot be completed offers nothing: %v", got)
	}
}

func TestBashCompletesPositionalArguments(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "disk.img"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := bashComplete(t, dir, "synctang-unlocker", "open", "di"); strings.Join(got, " ") != "disk.img" {
		t.Fatalf("open takes a device: %v", got)
	}
	if got := bashComplete(t, dir, "synctang-unlocker", "completion", ""); strings.Join(got, " ") != "bash fish zsh" {
		t.Fatalf("completion takes a shell: %v", got)
	}
}

// Descriptions come from the programs' own help text, which may hold anything.
func TestEveryShellSurvivesAwkwardDescriptions(t *testing.T) {
	awkward := `it's [odd]: "quoted" $HOME ` + "`tick` \\ back; & | < > (x) #hash"
	p := Program{Name: "synctang-unlocker", Commands: []Command{{
		Name: "enrol", Summary: awkward,
		Flags: []Flag{{Name: "name", Value: "string", Usage: awkward}, {Name: "yes", Usage: awkward}},
	}}}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		script, err := Generate(shell, p)
		if err != nil {
			t.Fatal(err)
		}
		bin := lookPath(t, shell)
		file := filepath.Join(t.TempDir(), "script")
		if err := os.WriteFile(file, []byte(script), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(bin, "-n", file).CombinedOutput(); err != nil {
			t.Errorf("%s -n: %v\n%s\n%s", shell, err, out, script)
		}
	}
}

func TestZshAndFishAreValidForTheSample(t *testing.T) {
	for _, shell := range []string{"zsh", "fish"} {
		script, err := Generate(shell, sample)
		if err != nil {
			t.Fatal(err)
		}
		bin := lookPath(t, shell)
		file := filepath.Join(t.TempDir(), "script")
		if err := os.WriteFile(file, []byte(script), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(bin, "-n", file).CombinedOutput(); err != nil {
			t.Errorf("%s -n: %v\n%s\n%s", shell, err, out, script)
		}
	}
	zsh, _ := Generate("zsh", sample)
	if !strings.HasPrefix(zsh, "#compdef synctang-unlocker\n") {
		t.Errorf("a zsh completion file starts with #compdef:\n%s", zsh)
	}
	fish, _ := Generate("fish", sample)
	for _, want := range []string{"complete -c synctang-unlocker", "-l device", "-l yes"} {
		if !strings.Contains(fish, want) {
			t.Errorf("fish output lacks %q:\n%s", want, fish)
		}
	}
}

func TestTheOutputIsTheSameEveryTime(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		a, _ := Generate(shell, sample)
		b, _ := Generate(shell, sample)
		if a != b || a == "" {
			t.Errorf("%s: not deterministic or empty (the packages are built twice and compared)", shell)
		}
	}
}

func TestGenerateRefusesAShellItDoesNotKnow(t *testing.T) {
	if _, err := Generate("powershell", sample); err == nil || !strings.Contains(err.Error(), "bash, zsh or fish") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunWritesTheScriptForTheShellNamed(t *testing.T) {
	var out, errOut strings.Builder
	help := func(cmd string) string {
		if cmd == "enrol" {
			return sampleHelp
		}
		return "Usage of " + cmd + ":\n"
	}
	if code := Run("synctang-unlocker", sampleUsage, help, nil, []string{"bash"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	for _, want := range []string{"enrol", "agent", "devices", "--existing-passphrase-file", "completion"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the bash script lacks %q:\n%s", want, out.String())
		}
	}
	if !strings.Contains(out.String(), "bash zsh fish") && !strings.Contains(out.String(), `"bash zsh fish"`) {
		// the completion command is in the usage text of the real programs; here it is not,
		// so only a command that is there gets a positional.
		t.Log("(this sample usage has no completion command)")
	}
}

func TestRunGivesTheCompletionCommandItsShellsAndLetsTheProgramAddMore(t *testing.T) {
	usage := sampleUsage + "  completion print a completion script\n"
	var out, errOut strings.Builder
	tweak := func(p *Program) {
		for i := range p.Commands {
			if p.Commands[i].Name == "open" {
				p.Commands[i].PositionalFiles = true
			}
		}
	}
	help := func(cmd string) string { return "Usage of " + cmd + ":\n" }
	if code := Run("synctang-unlocker", usage, help, tweak, []string{"bash"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), `words="bash zsh fish"`) || !strings.Contains(out.String(), "files=1") {
		t.Errorf("missing the completion command's shells or the program's own positional:\n%s", out.String())
	}
}

func TestRunNeedsExactlyOneKnownShell(t *testing.T) {
	help := func(cmd string) string { return "Usage of " + cmd + ":\n" }
	for _, args := range [][]string{nil, {"bash", "zsh"}, {"powershell"}} {
		var out, errOut strings.Builder
		if code := Run("synctang-unlocker", sampleUsage, help, nil, args, &out, &errOut); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
		if out.Len() != 0 || !strings.Contains(errOut.String(), "bash, zsh or fish") {
			t.Errorf("%v: stdout %q stderr %q", args, out.String(), errOut.String())
		}
	}
}

func TestOnlyGoesDefaultNoteIsDroppedFromTheDescription(t *testing.T) {
	help := "Usage of x:\n" +
		"  --name string\n    \tthis machine's name (default: the name already on the volume)\n" +
		"  --key-file string\n    \tpath to the key (default \"/home/someone/.config/k/key\")\n"
	got := ParseHelp(help)
	if got[0].Usage != "this machine's name (default: the name already on the volume)" {
		t.Errorf("a sentence of the author's that contains the word default is kept: %q", got[0].Usage)
	}
	if got[1].Usage != "path to the key" {
		t.Errorf("Go's own default note is dropped: %q", got[1].Usage)
	}
}

// A help text whose descriptions were wrapped (gnuflag does that) gives the same descriptions as one
// that was not.
func TestParseHelpJoinsWrappedDescriptions(t *testing.T) {
	flat := "  --store string\n    \tif there is no key yet, where to keep the one made: auto, file (default \"auto\")\n  --yes\n    \tanswer yes\n"
	wrapped := "  --store string\n      if there is no key yet, where to keep the one made: auto, file\n      (default \"auto\")\n  --yes\n      answer yes\n"
	a, b := ParseHelp(flat), ParseHelp(wrapped)
	if len(a) != 2 || len(b) != 2 {
		t.Fatalf("%+v %+v", a, b)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("flag %d: %+v from the flat text, %+v from the wrapped", i, a[i], b[i])
		}
	}
}

func TestUsageProblemsFindsWhatBreaksInATerminal(t *testing.T) {
	good := "usage: x\n\ncommands:\n  pair  enrol this key holder on a machine\n  init  generate a key and an identity\n        for it\n"
	if p := UsageProblems(good, 78); len(p) != 0 {
		t.Errorf("a good usage was faulted: %v", p)
	}
	long := "usage: x\n\ncommands:\n  pair  " + strings.Repeat("word ", 20) + "\n"
	if p := UsageProblems(long, 78); len(p) == 0 {
		t.Error("a line of more than 78 columns was not noticed")
	}
	crooked := "usage: x\n\ncommands:\n  pair  one\n  devices list\n"
	if p := UsageProblems(crooked, 78); len(p) == 0 {
		t.Error("descriptions that do not start in one column were not noticed")
	}
	continued := "usage: x\n\ncommands:\n  pair  one\n    two\n"
	if p := UsageProblems(continued, 78); len(p) == 0 {
		t.Error("a continuation that is not under the descriptions was not noticed")
	}
}
