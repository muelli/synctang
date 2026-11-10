// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"github.com/muelli/synctang/internal/ui"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/muelli/synctang/internal/gnuflag"
	"github.com/syncthing/syncthing/lib/protocol"
)

// The machines file is this key holder's address book: a name for each machine it can unlock, so that
// "synctang-keyholder unlock my-laptop" works without anybody remembering a transport id. One machine
// to a line, "name transport-id [discovery-url]"; blank lines and lines starting with # are ignored,
// and the file is meant to be edited by hand as well as by "machines" and "pair". It holds nothing
// secret.

type machine struct {
	Name      string
	ID        string
	Discovery string
}

func defaultMachinesFile() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "machines"), nil
}

func machinesFileFlag(fs *flag.FlagSet) *string {
	def, err := defaultMachinesFile()
	if err != nil {
		def = ""
	}
	return fs.String("machines-file", def, "file naming the machines this key holder can unlock")
}

var machineNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// validMachineName refuses what could not be told from a transport id (so that "unlock X" never has
// two meanings), and what would not survive being a word on a line.
func validMachineName(name string) error {
	if !machineNameRE.MatchString(name) {
		return fmt.Errorf("%q is not a usable machine name: use letters, digits, dots, dashes and underscores, starting with a letter or digit, at most 64", name)
	}
	if _, err := protocol.DeviceIDFromString(name); err == nil {
		return fmt.Errorf("%q is not a usable machine name: it is a transport id", name)
	}
	return nil
}

// checkDiscoveryURL refuses a discovery server that could not be one line of the address book: whitespace in it
// would split the line into more fields than there are, and the book would not read any more.
func checkDiscoveryURL(u string) error {
	if strings.ContainsFunc(u, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return errors.New("the discovery server in it has a space or a control character")
	}
	return nil
}

func canonicalMachineID(id string) (string, error) {
	got, err := protocol.DeviceIDFromString(strings.TrimSpace(id))
	if err != nil {
		return "", fmt.Errorf("%q is not a transport id (the machine prints it as \"this machine's transport id\"): %w", id, err)
	}
	return got.String(), nil
}

// loadMachines reads the file; one that is not there is an empty book.
func loadMachines(path string) ([]machine, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	defer f.Close()
	var out []machine
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || len(fields) > 3 {
			return nil, fmt.Errorf("%s: line %d: want \"name transport-id [discovery-url]\"", path, n)
		}
		if err := validMachineName(fields[0]); err != nil {
			return nil, fmt.Errorf("%s: line %d: %w", path, n, err)
		}
		id, err := canonicalMachineID(fields[1])
		if err != nil {
			return nil, fmt.Errorf("%s: line %d: %w", path, n, err)
		}
		m := machine{Name: fields[0], ID: id}
		if len(fields) == 3 {
			m.Discovery = fields[2]
		}
		out = append(out, m)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return out, nil
}

// saveMachines writes the whole file, sorted, through a temporary file and a rename.
func saveMachines(path string, ms []machine) error {
	sort.Slice(ms, func(i, j int) bool { return ms[i].Name < ms[j].Name })
	var b strings.Builder
	b.WriteString("# The machines this key holder can unlock: name, transport id, and optionally a discovery server.\n")
	for _, m := range ms {
		fmt.Fprintf(&b, "%s %s", m.Name, m.ID)
		if m.Discovery != "" {
			fmt.Fprintf(&b, " %s", m.Discovery)
		}
		b.WriteString("\n")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".machines-")
	if err != nil {
		return err
	}
	_, werr := tmp.WriteString(b.String())
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// putMachine adds m or, with replace, changes what its name stands for. The same name for the same
// id is not a change. It says whether it changed anything and what the name meant before.
func putMachine(path string, m machine, replace bool) (previous machine, replaced bool, err error) {
	if err := validMachineName(m.Name); err != nil {
		return machine{}, false, err
	}
	if err := checkDiscoveryURL(m.Discovery); err != nil {
		return machine{}, false, err
	}
	ms, err := loadMachines(path)
	if err != nil {
		return machine{}, false, err
	}
	for i, old := range ms {
		if old.Name != m.Name {
			continue
		}
		if old == m {
			return old, false, nil
		}
		if old.ID != m.ID && !replace {
			return old, false, fmt.Errorf("%s already names %s: use --force to make it name %s instead", m.Name, old.ID, m.ID)
		}
		ms[i] = m
		return old, true, saveMachines(path, ms)
	}
	return machine{}, false, saveMachines(path, append(ms, m))
}

// resolveMachine turns what a person typed after "unlock" into a machine: a transport id, which
// needs no book (the book is only asked for a discovery server it may hold for it), or a name.
func resolveMachine(path, arg string) (machine, error) {
	ms, err := loadMachines(path)
	if err != nil {
		return machine{}, err
	}
	if id, err := protocol.DeviceIDFromString(arg); err == nil {
		m := machine{ID: id.String()}
		for _, known := range ms {
			if known.ID == m.ID {
				return known, nil
			}
		}
		return m, nil
	}
	for _, m := range ms {
		if m.Name == arg {
			return m, nil
		}
	}
	if len(ms) == 0 {
		return machine{}, fmt.Errorf("%q is neither a transport id nor the name of a machine, and no machines are named yet: "+
			"pair with one (synctang-keyholder pair), or name one with: synctang-keyholder machines add <name> <transport id>", arg)
	}
	names := make([]string, len(ms))
	for i, m := range ms {
		names[i] = m.Name
	}
	maybe := ""
	if m, ok := ui.Closest(arg, names); ok {
		maybe = fmt.Sprintf("did you mean %s? ", m)
	}
	return machine{}, fmt.Errorf("%q is neither a transport id nor the name of a machine; %sthe names are: %s; name another with: synctang-keyholder machines add <name> <transport id>",
		arg, maybe, strings.Join(names, ", "))
}

func runMachines(args []string, stdout, stderr io.Writer) int {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("machines "+sub, flag.ContinueOnError)
	fs.SetOutput(gnuflag.Writer(stderr))
	file := machinesFileFlag(fs)
	// Both are for "add", and defined for every subcommand so that "machines -h" lists them.
	discovery := fs.String("discovery", "", "with add: the discovery server URL for this machine, if it does not use the public one")
	force := fs.Bool("force", false, "with add: make an existing name stand for this machine instead")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" {
		ui.Errorln(stderr, "--machines-file is required (could not determine a default)")
		return 2
	}
	switch sub {
	case "list":
		ms, err := loadMachines(*file)
		if err != nil {
			ui.Errorf(stderr, "%v\n", err)
			return 1
		}
		if len(ms) == 0 {
			fmt.Fprintln(stdout, "no machines are named yet: pair with one (synctang-keyholder pair), or name one with: synctang-keyholder machines add <name> <transport id>")
			return 0
		}
		look := ui.For(stdout)
		if look == (ui.Style{}) {
			// Plain: one machine to a line, as scripts have always read it.
			for _, m := range ms {
				fmt.Fprintf(stdout, "%s  %s", m.Name, m.ID)
				if m.Discovery != "" {
					fmt.Fprintf(stdout, "  %s", m.Discovery)
				}
				fmt.Fprintln(stdout)
			}
			return 0
		}
		rows := make([][]string, len(ms))
		for i, m := range ms {
			rows[i] = []string{look.Bold(m.Name), m.ID, look.Dim(m.Discovery)}
		}
		header := []string{"NAME", "TRANSPORT ID", "DISCOVERY"}
		fmt.Fprint(stdout, ui.Table(header, rows, look))
		return 0
	case "add":
		if fs.NArg() != 2 {
			ui.Errorln(stderr, "machines add wants a name and a transport id")
			return 2
		}
		name := fs.Arg(0)
		if err := validMachineName(name); err != nil {
			ui.Errorf(stderr, "%v\n", err)
			return 2
		}
		id, err := canonicalMachineID(fs.Arg(1))
		if err != nil {
			ui.Errorf(stderr, "%v\n", err)
			return 2
		}
		if _, _, err := putMachine(*file, machine{Name: name, ID: id, Discovery: *discovery}, *force); err != nil {
			ui.Errorf(stderr, "%v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "%s is %s; unlock it with: synctang-keyholder unlock %s\n", name, id, name)
		return 0
	case "remove":
		if fs.NArg() != 1 {
			ui.Errorln(stderr, "machines remove wants a name")
			return 2
		}
		ms, err := loadMachines(*file)
		if err != nil {
			ui.Errorf(stderr, "%v\n", err)
			return 1
		}
		for i, m := range ms {
			if m.Name == fs.Arg(0) {
				if err := saveMachines(*file, append(ms[:i], ms[i+1:]...)); err != nil {
					ui.Errorf(stderr, "%v\n", err)
					return 1
				}
				fmt.Fprintf(stdout, "removed %s (the machine still lets this key holder unlock it: this only forgets the name)\n", m.Name)
				return 0
			}
		}
		ui.Errorf(stderr, "no machine named %q\n", fs.Arg(0))
		return 1
	}
	ui.Errorf(stderr, "unknown machines command %q: want list, add or remove\n", sub)
	return 2
}
