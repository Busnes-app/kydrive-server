package drive_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// rerunners take a closure that runs again when PostgreSQL reports a serialization failure.
var rerunners = map[string]bool{"transaction": true, "Retry": true}

// A closure that a transaction may run again starts from the same state each run: every
// variable from outside it that it writes (bar err) is set in its leading statements, so a
// rolled-back run leaves nothing behind (a blob listed for removal, a flag, a count).
func TestRerunClosuresResetWhatTheyWrite(t *testing.T) {
	fset := token.NewFileSet()
	for _, dir := range []string{".", "../store"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, file, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !rerunners[calleeName(call)] {
					return true
				}
				for _, a := range call.Args {
					if lit, ok := a.(*ast.FuncLit); ok {
						for _, w := range unresetWrites(lit) {
							t.Errorf("%s: %s is written by a closure %s may rerun, but not reset at its start", fset.Position(w.Pos()), w.Name, calleeName(call))
						}
					}
				}
				return true
			})
		}
	}
}

func calleeName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fn.Sel.Name
	case *ast.Ident:
		return fn.Name
	}
	return ""
}

// unresetWrites are the captured variables lit writes after its leading statements without
// having set them there. Names declared in lit count as its own wherever they are declared.
func unresetWrites(lit *ast.FuncLit) []*ast.Ident {
	own := map[string]bool{"err": true, "_": true}
	for _, field := range lit.Type.Params.List {
		for _, name := range field.Names {
			own[name.Name] = true
		}
	}
	ast.Inspect(lit.Body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			if s.Tok == token.DEFINE {
				for _, l := range s.Lhs {
					if id, ok := l.(*ast.Ident); ok {
						own[id.Name] = true
					}
				}
			}
		case *ast.ValueSpec:
			for _, name := range s.Names {
				own[name.Name] = true
			}
		case *ast.RangeStmt:
			if s.Tok == token.DEFINE {
				for _, e := range []ast.Expr{s.Key, s.Value} {
					if id, ok := e.(*ast.Ident); ok {
						own[id.Name] = true
					}
				}
			}
		case *ast.FuncLit:
			if s != lit {
				for _, field := range s.Type.Params.List {
					for _, name := range field.Names {
						own[name.Name] = true
					}
				}
			}
		}
		return true
	})
	reset := map[string]bool{}
	body := lit.Body.List
	for len(body) > 0 {
		if d, ok := body[0].(*ast.DeclStmt); ok {
			_ = d
		} else if s, ok := body[0].(*ast.AssignStmt); ok && s.Tok == token.ASSIGN {
			for _, l := range s.Lhs {
				if id := rootIdent(l); id != nil {
					reset[id.Name] = true
				}
			}
		} else {
			break
		}
		body = body[1:]
	}
	var out []*ast.Ident
	check := func(e ast.Expr) {
		if id := rootIdent(e); id != nil && !own[id.Name] && !reset[id.Name] {
			out = append(out, id)
		}
	}
	for _, stmt := range body {
		ast.Inspect(stmt, func(n ast.Node) bool {
			switch s := n.(type) {
			case *ast.AssignStmt:
				if s.Tok != token.DEFINE {
					for _, l := range s.Lhs {
						check(l)
					}
				}
			case *ast.IncDecStmt:
				check(s.X)
			}
			return true
		})
	}
	return out
}

// rootIdent is the variable an assignment target belongs to: x for x, x.f, x[i] and x.f[i].
func rootIdent(e ast.Expr) *ast.Ident {
	for {
		switch v := e.(type) {
		case *ast.Ident:
			return v
		case *ast.SelectorExpr:
			e = v.X
		case *ast.IndexExpr:
			e = v.X
		case *ast.StarExpr:
			e = v.X
		default:
			return nil
		}
	}
}
