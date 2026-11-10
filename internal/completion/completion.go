// SPDX-License-Identifier: AGPL-3.0-or-later

// Package completion writes shell completion scripts (bash, zsh and fish) for a program
// from the program's own text: the command list in its usage message and the flag list each
// command prints for -h. Nothing else describes the commands and flags, so a completion
// script made this way lists exactly what the program it came from has, and cannot say
// anything that the program does not.
package completion

import (
	"flag"
	"fmt"
	"github.com/muelli/synctang/internal/gnuflag"
	"io"
	"regexp"
	"strings"
)

// Flag is one flag of a command. Value is the type its help names ("string", "duration"),
// empty for a flag that takes none.
type Flag struct {
	Name  string
	Value string
	Usage string
}

// TakesFile reports whether the value of f is a path: a device node, a file or a directory.
// Decided from the name, since the help text names only the type.
func (f Flag) TakesFile() bool {
	if f.Value == "" {
		return false
	}
	if f.Name == "device" {
		return true
	}
	for _, part := range []string{"file", "conf", "dir", "path"} {
		if strings.Contains(f.Name, part) {
			return true
		}
	}
	return false
}

// Command is one subcommand. Positional lists the words its argument may be, and
// PositionalFiles says the argument is a path.
type Command struct {
	Name            string
	Summary         string
	Flags           []Flag
	Positional      []string
	PositionalFiles bool
}

// Program is what a completion script is made from.
type Program struct {
	Name     string
	Commands []Command
}

// ParseUsage reads the commands from a usage message: the entries under "commands:", each
// a name and a summary that may continue on lines indented further.
func ParseUsage(usage string) []Command {
	var cmds []Command
	in := false
	for _, line := range strings.Split(usage, "\n") {
		switch {
		case strings.HasPrefix(line, "commands:"):
			in = true
		case !in:
		case strings.TrimSpace(line) == "":
			in = false
		case strings.HasPrefix(line, "   ") && len(cmds) > 0:
			cmds[len(cmds)-1].Summary += " " + strings.TrimSpace(line)
		case strings.HasPrefix(line, "  "):
			name, rest, _ := strings.Cut(strings.TrimSpace(line), " ")
			cmds = append(cmds, Command{Name: name, Summary: strings.TrimSpace(rest)})
		}
	}
	return cmds
}

// goDefault is the note the flag package appends to a usage, "(default 2m0s)" or, for a path,
// "(default "/home/someone/.config/...")". Not "(default: ...)", which a person wrote.
var goDefault = regexp.MustCompile(`\s\(default [^)]*\)$`)

var helpFlagLine = regexp.MustCompile(`^  -{1,2}([^\s-][^\s]*)(?: (\S+))?$`)

// ParseHelp reads the flags from what a flag set prints for -h: a line "  -name type"
// (or just "  -name" for a flag with no value) and its usage on the next line, indented
// with a tab.
func ParseHelp(help string) []Flag {
	var flags []Flag
	for _, line := range strings.Split(help, "\n") {
		if m := helpFlagLine.FindStringSubmatch(line); m != nil {
			flags = append(flags, Flag{Name: m[1], Value: m[2]})
			continue
		}
		// A description under its option: as the flag package writes it, or wrapped by gnuflag.
		if (strings.HasPrefix(line, "    \t") || strings.HasPrefix(line, "      ")) && len(flags) > 0 {
			f := &flags[len(flags)-1]
			if f.Usage != "" {
				f.Usage += " "
			}
			f.Usage += strings.TrimSpace(line)
		}
	}
	// A default is whatever it was on the machine that ran the program. A completion script is
	// written once, when a package is built, and installed on others, so it carries none: a path
	// from the builder's home directory would be wrong there, and would make the same commit build
	// to different packages on different machines.
	for i := range flags {
		flags[i].Usage = goDefault.ReplaceAllString(flags[i].Usage, "")
	}
	return flags
}

// Build makes a Program from a usage message and a function that returns each command's -h
// output.
func Build(name, usage string, helpOf func(command string) string) Program {
	p := Program{Name: name, Commands: ParseUsage(usage)}
	for i := range p.Commands {
		p.Commands[i].Flags = ParseHelp(helpOf(p.Commands[i].Name))
	}
	return p
}

// Generate writes the completion script for shell, which is "bash", "zsh" or "fish".
func Generate(shell string, p Program) (string, error) {
	switch shell {
	case "bash":
		return bash(p), nil
	case "zsh":
		return zsh(p), nil
	case "fish":
		return fish(p), nil
	}
	return "", fmt.Errorf("unknown shell %q: want bash, zsh or fish", shell)
}

const header = `Written by "%s completion %s" from the program's own usage and flag help, so it
lists exactly the commands and flags this version has. Do not edit it.`

func commentHeader(p Program, shell string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s completion for %s.\n", shell, p.Name)
	for _, l := range strings.Split(fmt.Sprintf(header, p.Name, shell), "\n") {
		b.WriteString("# " + l + "\n")
	}
	return b.String()
}

// functionName is a shell function name for the program: letters, digits and underscores.
func functionName(p Program) string {
	return "_" + strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, p.Name)
}

func commandNames(p Program) []string {
	names := make([]string, len(p.Commands))
	for i, c := range p.Commands {
		names[i] = c.Name
	}
	return names
}

func flagNames(c Command) []string {
	var names []string
	for _, f := range c.Flags {
		names = append(names, "--"+f.Name)
	}
	return names
}

func bash(p Program) string {
	var b strings.Builder
	b.WriteString(commentHeader(p, "bash"))
	fn := functionName(p)
	fmt.Fprintf(&b, "%s() {\n", fn)
	b.WriteString(`	COMPREPLY=()
	local cur=${COMP_WORDS[COMP_CWORD]} prev=""
	[ "$COMP_CWORD" -ge 1 ] && prev=${COMP_WORDS[COMP_CWORD-1]}
	if [ "$COMP_CWORD" -eq 1 ]; then
`)
	fmt.Fprintf(&b, "\t\tmapfile -t COMPREPLY < <(compgen -W %q -- \"$cur\")\n", strings.Join(commandNames(p), " "))
	b.WriteString(`		return
	fi
	local flags="" words="" files=""
	case "${COMP_WORDS[1]}" in
`)
	for _, c := range p.Commands {
		fmt.Fprintf(&b, "\t%s)\n", c.Name)
		if len(c.Flags) > 0 {
			fmt.Fprintf(&b, "\t\tflags=%q\n", strings.Join(flagNames(c), " "))
		}
		var fileFlags, otherFlags []string
		for _, f := range c.Flags {
			switch {
			case f.TakesFile():
				fileFlags = append(fileFlags, "--"+f.Name)
			case f.Value != "":
				otherFlags = append(otherFlags, "--"+f.Name)
			}
		}
		if len(fileFlags) > 0 || len(otherFlags) > 0 {
			b.WriteString("\t\tcase \"$prev\" in\n")
			if len(fileFlags) > 0 {
				fmt.Fprintf(&b, "\t\t%s)\n\t\t\tmapfile -t COMPREPLY < <(compgen -f -- \"$cur\")\n\t\t\treturn ;;\n", strings.Join(fileFlags, "|"))
			}
			if len(otherFlags) > 0 {
				fmt.Fprintf(&b, "\t\t%s)\n\t\t\treturn ;;\n", strings.Join(otherFlags, "|"))
			}
			b.WriteString("\t\tesac\n")
		}
		if len(c.Positional) > 0 {
			fmt.Fprintf(&b, "\t\twords=%q\n", strings.Join(c.Positional, " "))
		}
		if c.PositionalFiles {
			b.WriteString("\t\tfiles=1\n")
		}
		b.WriteString("\t\t;;\n")
	}
	b.WriteString(`	*)
		return ;;
	esac
	if [[ $cur == -* ]]; then
		mapfile -t COMPREPLY < <(compgen -W "$flags" -- "$cur")
	elif [ -n "$words" ]; then
		mapfile -t COMPREPLY < <(compgen -W "$words" -- "$cur")
	elif [ -n "$files" ]; then
		mapfile -t COMPREPLY < <(compgen -f -- "$cur")
	fi
}
`)
	fmt.Fprintf(&b, "complete -o filenames -F %s %s\n", fn, p.Name)
	return b.String()
}

// zshQuote puts s in single quotes for the shell.
func zshQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// zshSpecText escapes text that goes inside an _arguments specification, where a backslash,
// a bracket and a colon are special.
func zshSpecText(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`, `:`, `\:`)
	return r.Replace(s)
}

// zshDescribeText escapes the colon in a _describe entry's name; the description after the
// first colon may hold anything.
func zshDescribeEntry(name, summary string) string {
	return zshQuote(strings.ReplaceAll(name, ":", `\:`) + ":" + strings.ReplaceAll(summary, `\`, `\\`))
}

func zsh(p Program) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#compdef %s\n", p.Name)
	b.WriteString(commentHeader(p, "zsh"))
	fn := functionName(p)
	fmt.Fprintf(&b, "%s() {\n\tlocal -a commands\n\tcommands=(\n", fn)
	for _, c := range p.Commands {
		fmt.Fprintf(&b, "\t\t%s\n", zshDescribeEntry(c.Name, c.Summary))
	}
	fmt.Fprintf(&b, "\t)\n\tif (( CURRENT == 2 )); then\n\t\t_describe -t commands %s commands\n\t\treturn\n\tfi\n\tcase ${words[2]} in\n", zshQuote(p.Name+" command"))
	for _, c := range p.Commands {
		if len(c.Flags) == 0 && len(c.Positional) == 0 && !c.PositionalFiles {
			continue
		}
		fmt.Fprintf(&b, "\t%s)\n\t\t_arguments", c.Name)
		for _, f := range c.Flags {
			spec := "--" + f.Name
			switch {
			case f.TakesFile():
				spec += "=[" + zshSpecText(f.Usage) + "]:" + zshSpecText(f.Name) + ":_files"
			case f.Value != "":
				spec += "=[" + zshSpecText(f.Usage) + "]:" + zshSpecText(f.Name) + ":"
			default:
				spec += "[" + zshSpecText(f.Usage) + "]"
			}
			fmt.Fprintf(&b, " \\\n\t\t\t%s", zshQuote(spec))
		}
		switch {
		case len(c.Positional) > 0:
			fmt.Fprintf(&b, " \\\n\t\t\t%s", zshQuote("1:argument:("+strings.Join(c.Positional, " ")+")"))
		case c.PositionalFiles:
			fmt.Fprintf(&b, " \\\n\t\t\t%s", zshQuote("*:argument:_files"))
		}
		b.WriteString(" ;;\n")
	}
	fmt.Fprintf(&b, "\tesac\n}\n%s \"$@\"\n", fn)
	return b.String()
}

// fishQuote puts s in single quotes for fish, where only a backslash and a single quote
// are special inside them.
func fishQuote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

func fish(p Program) string {
	var b strings.Builder
	b.WriteString(commentHeader(p, "fish"))
	fmt.Fprintf(&b, "complete -c %s -f\n", p.Name)
	for _, c := range p.Commands {
		fmt.Fprintf(&b, "complete -c %s -n __fish_use_subcommand -a %s -d %s\n", p.Name, c.Name, fishQuote(c.Summary))
	}
	for _, c := range p.Commands {
		when := "__fish_seen_subcommand_from " + c.Name
		for _, f := range c.Flags {
			kind := ""
			switch {
			case f.TakesFile():
				kind = " -r -F"
			case f.Value != "":
				kind = " -x"
			}
			fmt.Fprintf(&b, "complete -c %s -n %s -l %s%s -d %s\n", p.Name, fishQuote(when), f.Name, kind, fishQuote(f.Usage))
		}
		if len(c.Positional) > 0 {
			fmt.Fprintf(&b, "complete -c %s -n %s -a %s\n", p.Name, fishQuote(when), fishQuote(strings.Join(c.Positional, " ")))
		}
		if c.PositionalFiles {
			fmt.Fprintf(&b, "complete -c %s -n %s -F\n", p.Name, fishQuote(when))
		}
	}
	return b.String()
}

// Run is a program's "completion" command: it builds the program from its usage message
// and each command's -h output (helpOf must not itself call the completion command), lets
// tweak add what only the program knows, and writes the script for the one shell named in
// args. It returns the exit status.
func Run(name, usage string, helpOf func(command string) string, tweak func(*Program), args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("completion", flag.ContinueOnError)
	fs.SetOutput(gnuflag.Writer(stderr))
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintf(stderr, "usage: %s completion bash|zsh|fish\n%s: completion needs one shell: bash, zsh or fish\n", name, name)
		return 2
	}
	p := Build(name, usage, helpOf)
	for i := range p.Commands {
		if p.Commands[i].Name == "completion" {
			p.Commands[i].Positional = []string{"bash", "zsh", "fish"}
		}
	}
	if tweak != nil {
		tweak(&p)
	}
	script, err := Generate(fs.Arg(0), p)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 2
	}
	fmt.Fprint(stdout, script)
	return 0
}

// UsageProblems says what is wrong with the layout of a program's usage text for a terminal that is
// width columns wide: a line too long (which the terminal breaks, leaving the rest at the left margin),
// descriptions of commands that do not all start in one column, and a continuation line that is not
// under them. A usage that is right gives none.
func UsageProblems(usage string, width int) []string {
	var problems []string
	column := -1
	in := false
	for _, line := range strings.Split(usage, "\n") {
		if len(line) > width {
			problems = append(problems, fmt.Sprintf("%d columns, want at most %d: %q", len(line), width, line))
		}
		switch {
		case strings.HasPrefix(line, "commands:"):
			in = true
		case !in:
		case strings.TrimSpace(line) == "":
			in = false
		default:
			trimmed := strings.TrimLeft(line, " ")
			indent := len(line) - len(trimmed)
			if indent == 2 {
				_, rest, _ := strings.Cut(trimmed, " ")
				start := len(line) - len(strings.TrimLeft(rest, " ")) // where the description begins
				if column == -1 {
					column = start
				} else if start != column {
					problems = append(problems, fmt.Sprintf("the description starts in column %d here and %d above: %q", start, column, line))
				}
			} else if indent != column {
				problems = append(problems, fmt.Sprintf("a continuation in column %d, under descriptions that start in column %d: %q", indent, column, line))
			}
		}
	}
	return problems
}
