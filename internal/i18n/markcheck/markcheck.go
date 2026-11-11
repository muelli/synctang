// SPDX-License-Identifier: AGPL-3.0-or-later

// Package markcheck finds the messages in a program's source that are shown to a person and are not marked
// with i18n.T. A test in each program uses it, so that a message added later cannot be left out.
//
// Which messages count is decided by where they are passed: the text given to the printing functions of
// package ui, to fmt.Fprint, fmt.Fprintf and fmt.Fprintln, to fmt.Errorf and errors.New (an error is printed to
// the person by the command that gets it), the description of a flag, a table's column headers, and the text of
// a line made by ui.Style.Line. A literal that has no word in it (a format such as "%s: %v\n", a separator) is
// not a message. A string that is not passed to one of those, such as a key or a file name, is not looked at.
//
// Imports only the standard library, and only tests import it, so the programs do not carry the parser.
package markcheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Unit is a piece of source that should be wrapped in T( ... ): one string literal, or several that are
// joined with + and make one message.
type Unit struct {
	File       string
	Line       int
	Start, End int    // byte offsets in File, End exclusive
	Text       string // the message, as the compiler sees it
	// ZeroArg: the unit is the format of a printf-style call that has nothing to format. With T around the
	// format, go vet reports a non-constant format string, so the call has to change (see the rewrite).
	ZeroArg bool
	// Call is the function the message is passed to, such as "fmt.Fprintf", and FunStart and FunEnd are where
	// its name is in the source.
	Call             string
	FunStart, FunEnd int
	// CallStart and CallEnd are the whole call, for a fmt.Sprintf with nothing to format, which is replaced
	// by the marked text alone.
	CallStart, CallEnd int
	// InConst: the text is in a constant declaration, where a call cannot be made; it is changed to a variable by hand.
	InConst bool
	// Prose: found because it reads as a sentence, not because of where it is passed.
	Prose bool
}

func (u Unit) String() string { return fmt.Sprintf("%s:%d: %s %q", u.File, u.Line, u.Call, u.Text) }

// Find parses the non-test Go files of dir and returns the messages not yet marked, in file and source order.
// A message is text that is passed to a function that shows it (see the package comment), and any other
// string constant that reads as a sentence (three words or more), except where it is compared with, searched
// for in or matched against other text, or written to a log (the journal is not for the person at the terminal).
// A line with i18n:skip in it, or the line before it, is left alone.
func Find(dir string) ([]Unit, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var out []Unit
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		units := unitsIn(fset, file, path)
		seen := map[[2]int]bool{}
		for _, u := range units {
			seen[[2]int{u.Start, u.End}] = true
		}
		for _, u := range proseIn(fset, file, path) {
			if !seen[[2]int{u.Start, u.End}] {
				units = append(units, u)
			}
		}
		units = dropSkipped(units, src)
		sort.SliceStable(units, func(i, j int) bool { return units[i].Start < units[j].Start })
		out = append(out, units...)
	}
	return out, nil
}

// dropSkipped removes the units on a line that says i18n:skip, or on the line after one.
func dropSkipped(units []Unit, src []byte) []Unit {
	lines := strings.Split(string(src), "\n")
	var out []Unit
	for _, u := range units {
		skipped := false
		for _, l := range []int{u.Line - 1, u.Line - 2} {
			if l >= 0 && l < len(lines) && strings.Contains(lines[l], "i18n:skip") {
				skipped = true
			}
		}
		if !skipped {
			out = append(out, u)
		}
	}
	return out
}

// verb is a printf verb; what is left of a format without them and without punctuation is the words.
var verb = regexp.MustCompile(`%[-+# 0-9.*]*[a-zA-Z%]`)
var word = regexp.MustCompile(`[A-Za-z]{2,}`)

func hasWords(s string) bool { return word.MatchString(verb.ReplaceAllString(s, " ")) }

func unitsIn(fset *token.FileSet, file *ast.File, path string) []Unit {
	var out []Unit
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name, indexes, printf := messageArgs(call)
		if name == "" {
			return true
		}
		for _, i := range indexes {
			for _, u := range unitsOf(call.Args[i]) {
				text, ok := constText(u)
				if !ok || !hasWords(text) {
					continue
				}
				out = append(out, callUnit(fset, call, path, name, printf, i, u, text))
			}
		}
		return true
	})
	return out
}

func callUnit(fset *token.FileSet, call *ast.CallExpr, path, name string, printf bool, i int, u ast.Expr, text string) Unit {
	start, end := fset.Position(u.Pos()), fset.Position(u.End())
	fun := call.Fun
	return Unit{
		File: path, Line: start.Line, Start: start.Offset, End: end.Offset, Text: text,
		ZeroArg: printf && len(call.Args) == i+1, Call: name,
		FunStart: fset.Position(fun.Pos()).Offset, FunEnd: fset.Position(fun.End()).Offset,
		CallStart: fset.Position(call.Pos()).Offset, CallEnd: fset.Position(call.End()).Offset,
	}
}

// proseIn finds the string constants that read as sentences wherever they are, but not where they are not
// shown: compared or searched for, matched, or written to a log, and not what is already marked.
func proseIn(fset *token.FileSet, file *ast.File, path string) []Unit {
	var out []Unit
	var stack []ast.Node
	inConst := 0
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			top := stack[len(stack)-1]
			if gd, ok := top.(*ast.GenDecl); ok && gd.Tok == token.CONST {
				inConst--
			}
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		if gd, ok := n.(*ast.GenDecl); ok && gd.Tok == token.CONST {
			inConst++
		}
		var expr ast.Expr
		switch x := n.(type) {
		case *ast.BasicLit:
			if x.Kind == token.STRING {
				expr = x
			}
		case *ast.BinaryExpr:
			if _, ok := constText(x); ok {
				expr = x
			}
		}
		if expr == nil {
			return true
		}
		// Returning false skips the children and the call with nil that pops the stack, so pop here.
		pop := func() { stack = stack[:len(stack)-1] }
		text, ok := constText(expr)
		if !ok || !reads(text) {
			pop()
			return false
		}
		if notShown(stack[:len(stack)-1]) {
			pop()
			return false
		}
		start, end := fset.Position(expr.Pos()), fset.Position(expr.End())
		u := Unit{File: path, Line: start.Line, Start: start.Offset, End: end.Offset, Text: text,
			Call: "text", InConst: inConst > 0, Prose: true}
		// fmt.Sprintf with nothing to format: the call is replaced by the marked text.
		if len(stack) >= 2 {
			if call, ok := stack[len(stack)-2].(*ast.CallExpr); ok && len(call.Args) >= 1 && call.Args[0] == expr {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					if recv, ok := sel.X.(*ast.Ident); ok && recv.Name == "fmt" && sel.Sel.Name == "Sprintf" {
						u.Call, u.ZeroArg = "fmt.Sprintf", len(call.Args) == 1
						u.CallStart, u.CallEnd = fset.Position(call.Pos()).Offset, fset.Position(call.End()).Offset
					}
				}
			}
		}
		out = append(out, u)
		// A chain of literals is one message: its parts are not looked at again.
		pop()
		return false
	})
	return out
}

// reads says whether text is prose: three words or more, with spaces between them. A path, a flag or an
// identifier has no spaces, and is not a message.
func reads(text string) bool {
	plain := verb.ReplaceAllString(text, " ")
	return len(word.FindAllString(plain, -1)) >= 3 && strings.Count(strings.TrimSpace(text), " ") >= 2
}

// notShown says whether text found under these ancestors is not for a person: it is compared, searched for,
// matched, logged, or already marked.
func notShown(ancestors []ast.Node) bool {
	for i := len(ancestors) - 1; i >= 0; i-- {
		switch a := ancestors[i].(type) {
		case *ast.CallExpr:
			if id, ok := a.Fun.(*ast.Ident); ok && id.Name == "T" {
				return true
			}
			sel, ok := a.Fun.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			recv, _ := sel.X.(*ast.Ident)
			if recv == nil {
				continue
			}
			if recv.Name == "i18n" && sel.Sel.Name == "T" {
				return true
			}
			switch recv.Name {
			case "strings", "bytes", "regexp", "slog", "log", "logger", "filepath", "path", "os", "exec", "strconv", "testing":
				return true
			case "ui":
				if sel.Sel.Name == "Debugf" {
					return true
				}
			}
			if sel.Sel.Name == "Log" || sel.Sel.Name == "Debug" || sel.Sel.Name == "Warn" && recv.Name == "logger" {
				return true
			}
		case *ast.BinaryExpr:
			if a.Op == token.EQL || a.Op == token.NEQ {
				return true
			}
		case *ast.CaseClause:
			return true
		case *ast.ImportSpec:
			return true
		case *ast.Field:
			return true // a struct tag
		}
	}
	return false
}

// messageArgs says which arguments of the call are messages: the name of the function (empty if it is not
// one that is looked at), the indexes of those arguments, and whether it is printf-style.
func messageArgs(call *ast.CallExpr) (name string, indexes []int, printf bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", nil, false
	}
	recv, _ := sel.X.(*ast.Ident)
	receiver := ""
	if recv != nil {
		receiver = recv.Name
	}
	n := len(call.Args)
	from := func(i int) []int {
		var is []int
		for ; i < n; i++ {
			is = append(is, i)
		}
		return is
	}
	switch receiver + "." + sel.Sel.Name {
	case "ui.Errorf", "ui.Notef", "ui.Infof":
		return receiver + "." + sel.Sel.Name, from(1)[:min(1, max(n-1, 0))], true
	case "ui.Errorln", "ui.Noteln", "ui.Infoln", "ui.Successln":
		return receiver + "." + sel.Sel.Name, from(1)[:min(1, max(n-1, 0))], false
	case "fmt.Fprintf":
		return "fmt.Fprintf", from(1)[:min(1, max(n-1, 0))], true
	case "fmt.Fprintln", "fmt.Fprint":
		return receiver + "." + sel.Sel.Name, from(1), false
	case "fmt.Errorf":
		return "fmt.Errorf", from(0)[:min(1, n)], true
	case "errors.New":
		return "errors.New", from(0)[:min(1, n)], false
	case "ui.Table":
		if n > 0 {
			return "ui.Table", []int{0}, false
		}
	}
	switch sel.Sel.Name {
	case "String", "Bool", "Duration", "Int", "Int64", "Uint", "Float64":
		// A flag: the description is the last argument, after the name and the default.
		if receiver == "fs" && n == 3 {
			return "fs." + sel.Sel.Name, []int{2}, false
		}
	case "Line":
		if n == 2 {
			return receiver + ".Line", []int{1}, false
		}
	}
	return "", nil, false
}

// unitsOf returns the pieces of e to wrap: a string literal; a chain of literals joined with + as one piece;
// in a chain that also has other operands, each run of literals; and, for a composite literal (a table's header),
// those of its elements. An expression that is already T(...) or is not text gives none.
func unitsOf(e ast.Expr) []ast.Expr {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			return []ast.Expr{x}
		}
	case *ast.ParenExpr:
		return unitsOf(x.X)
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return nil
		}
		if _, ok := constText(x); ok {
			return []ast.Expr{x}
		}
		var out []ast.Expr
		for _, side := range []ast.Expr{x.X, x.Y} {
			out = append(out, unitsOf(side)...)
		}
		return out
	case *ast.CompositeLit:
		var out []ast.Expr
		for _, el := range x.Elts {
			out = append(out, unitsOf(el)...)
		}
		return out
	}
	return nil
}

// constText is the value of e if it is a string literal or literals joined with +.
func constText(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(x.Value)
		return s, err == nil
	case *ast.ParenExpr:
		return constText(x.X)
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return "", false
		}
		a, ok := constText(x.X)
		if !ok {
			return "", false
		}
		b, ok := constText(x.Y)
		return a + b, ok
	}
	return "", false
}

// Dynamic returns where T is called with something that is not a string constant (a variable, a call): such a
// message cannot be collected from the source, so it cannot be translated. It reads the non-test files of dir.
func Dynamic(dir string) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var out []string
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 {
				return true
			}
			isT := false
			switch f := call.Fun.(type) {
			case *ast.Ident:
				isT = f.Name == "T"
			case *ast.SelectorExpr:
				if id, ok := f.X.(*ast.Ident); ok {
					isT = id.Name == "i18n" && f.Sel.Name == "T"
				}
			}
			if !isT {
				return true
			}
			if _, ok := constText(call.Args[0]); !ok {
				out = append(out, fmt.Sprintf("%s:%d: T is called with something that is not a string constant", path, fset.Position(call.Pos()).Line))
			}
			return true
		})
	}
	return out, nil
}
