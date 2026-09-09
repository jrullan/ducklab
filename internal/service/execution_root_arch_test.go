package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Re-entry used to execute a worktree run against the registered checkout.
// Keep the complete execution boundary mechanically enumerable: entry points
// that own root selection must call runRoot, and every test-first caller must
// pass runRoot rather than the registry path directly.
func TestWorktreeExecutionEntryPointsResolveRunRoot(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Clean(name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, file)
	}

	rootOwners := map[string]bool{"executeDryRun": false, "executeRun": false}
	testFirstCalls := 0
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if _, ownsRoot := rootOwners[fn.Name.Name]; ownsRoot {
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if ok && calledFunction(call) == "runRoot" {
						rootOwners[fn.Name.Name] = true
					}
					return true
				})
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || calledFunction(call) != "executeTestFirst" {
					return true
				}
				testFirstCalls++
				if len(call.Args) < 3 {
					t.Errorf("%s calls executeTestFirst without a project root argument", fn.Name.Name)
					return true
				}
				rootCall, ok := call.Args[2].(*ast.CallExpr)
				if !ok || calledFunction(rootCall) != "runRoot" {
					t.Errorf("%s calls executeTestFirst without runRoot as its project root", fn.Name.Name)
				}
				return true
			})
		}
	}
	for name, resolved := range rootOwners {
		if !resolved {
			t.Errorf("%s does not resolve its execution checkout through runRoot", name)
		}
	}
	if testFirstCalls == 0 {
		t.Fatal("no executeTestFirst call sites found; execution-root audit is no longer covering the boundary")
	}
}

func calledFunction(call *ast.CallExpr) string { return calledFunctionExpr(call.Fun) }

func calledFunctionExpr(expr ast.Expr) string {
	switch fun := expr.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	default:
		return ""
	}
}
