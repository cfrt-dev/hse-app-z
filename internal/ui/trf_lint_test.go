package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// formatVerbs returns the verbs of a printf format ("%d", "%.0f" → 'd',
// 'f'), ignoring "%%".
func formatVerbs(f string) string {
	var verbs []byte
	for i := 0; i < len(f); i++ {
		if f[i] != '%' {
			continue
		}
		i++
		for i < len(f) && strings.IndexByte("+-# 0123456789.*[]", f[i]) >= 0 {
			i++
		}
		if i < len(f) && f[i] != '%' {
			verbs = append(verbs, f[i])
		}
	}
	return string(verbs)
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

func funcName(e ast.Expr) string {
	switch f := e.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

// printfLike maps functions taking a format to the index of that argument.
var printfLike = map[string]int{
	"Info": 0, "OK": 0, "Warn": 0, // ui status helpers
	"Sprintf": 0, "Errorf": 0, "Printf": 0, "Fprintf": 1,
}

// TestTranslatedFormats statically checks every Trf(en, ru, args...) call,
// and every printf-style call whose format is Tr(en, ru): both formats must
// use the same verbs in the same order, and exactly as many as there are
// arguments. A mismatch prints "%!(EXTRA …)" or "%!d(MISSING)" in one of
// the languages only — go vet can't see it because the format is chosen at
// run time.
func TestTranslatedFormats(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root not found at %s", root)
	}
	fset := token.NewFileSet()
	checked := 0
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "testdata" || name == "vendor" || (strings.HasPrefix(name, ".") && path != root) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			check := func(en, ru string, nargs int) {
				checked++
				pos := fset.Position(call.Pos())
				ve, vr := formatVerbs(en), formatVerbs(ru)
				if ve != vr {
					t.Errorf("%s: verbs differ: en %q has %q, ru %q has %q", pos, en, ve, ru, vr)
				}
				if call.Ellipsis == token.NoPos && nargs >= 0 && len(ve) != nargs {
					t.Errorf("%s: %q uses %d verbs but gets %d arguments", pos, en, len(ve), nargs)
				}
			}
			name := funcName(call.Fun)
			if name == "Trf" && len(call.Args) >= 2 {
				en, ok1 := stringLit(call.Args[0])
				ru, ok2 := stringLit(call.Args[1])
				if ok1 && ok2 {
					check(en, ru, len(call.Args)-2)
				}
				return true
			}
			if idx, ok := printfLike[name]; ok && len(call.Args) > idx {
				inner, ok := call.Args[idx].(*ast.CallExpr)
				if ok && funcName(inner.Fun) == "Tr" && len(inner.Args) == 2 {
					en, ok1 := stringLit(inner.Args[0])
					ru, ok2 := stringLit(inner.Args[1])
					if ok1 && ok2 {
						check(en, ru, len(call.Args)-idx-1)
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 20 {
		t.Errorf("only %d translated formats found — is the scan broken?", checked)
	}
}

func TestFormatVerbs(t *testing.T) {
	for in, want := range map[string]string{
		"in %d days":       "d",
		"top %.0f%%":       "f",
		"%d/%d graded":     "dd",
		"100%% sure":       "",
		"%s за «%s»":       "ss",
		"%-10s|%+d|%#x|%q": "sdxq",
	} {
		if got := formatVerbs(in); got != want {
			t.Errorf("formatVerbs(%q) = %q, want %q", in, got, want)
		}
	}
}
