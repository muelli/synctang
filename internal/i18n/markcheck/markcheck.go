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
}

func (u Unit) String() string { return fmt.Sprintf("%s:%d: %s %q", u.File, u.Line, u.Call, u.Text) }

// Find parses the non-test Go files of dir and returns the messages not yet marked, in file and source order.
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
		out = append(out, unitsIn(fset, file, path)...)
	}
	return out, nil
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
				start, end := fset.Position(u.Pos()), fset.Position(u.End())
				text, ok := constText(u)
				if !ok || !hasWords(text) {
					continue
				}
				fun := call.Fun
				out = append(out, Unit{
					File: path, Line: start.Line, Start: start.Offset, End: end.Offset, Text: text,
					ZeroArg: printf && len(call.Args) == i+1, Call: name,
					FunStart: fset.Position(fun.Pos()).Offset, FunEnd: fset.Position(fun.End()).Offset,
				})
			}
		}
		return true
	})
	return out
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
