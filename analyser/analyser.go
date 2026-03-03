package analyser

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

var Analyzer = &analysis.Analyzer{
	Name: "goslicecheck",
	Doc:  "detects slices that can be preallocated",
	Run:  run,
}

func run(pass *analysis.Pass) (interface{}, error) {

	for _, file := range pass.Files {

		// Build a list of statements at the file's top-level function bodies
		// so we can look at the statement before a range loop.
		ast.Inspect(file, func(n ast.Node) bool {

			// We want to visit block statements so we can inspect neighbours.
			block, ok := n.(*ast.BlockStmt)
			if !ok {
				return true
			}

			for i, stmt := range block.List {

				// Look for range loops
				rangeStmt, ok := stmt.(*ast.RangeStmt)
				if !ok {
					continue
				}

				iterExpr := rangeStmt.X

				// Check if iterated type is slice
				iterType := pass.TypesInfo.TypeOf(iterExpr)
				if iterType == nil {
					continue
				}

				_, isSlice := iterType.(*types.Slice)
				if !isSlice {
					continue
				}

				// Look inside loop body for append pattern
				targetName, found := findAppendTarget(rangeStmt)
				if !found {
					continue
				}

				iterStr := exprString(iterExpr)

				// Skip if the slice is already preallocated with make(..., len(<iterExpr>))
				if isAlreadyPreallocated(block.List, i, targetName, iterStr) {
					continue
				}

				// Find the element type of the target slice so we can build make([]T, 0, len(...))
				elemType := findSliceElemType(pass, rangeStmt, targetName)

				makeExpr := fmt.Sprintf("make([]%s, 0, len(%s))", elemType, iterStr)

				// Try to find and replace the declaration of targetName just before the range loop.
				fix := buildFix(pass, block.List, i, targetName, makeExpr, rangeStmt.Pos())

				pass.Report(analysis.Diagnostic{
					Pos:            rangeStmt.Pos(),
					Message:        fmt.Sprintf("slice '%s' can be preallocated with capacity len(%s)", targetName, iterStr),
					SuggestedFixes: []analysis.SuggestedFix{fix},
				})
			}

			return true
		})
	}

	return nil, nil
}

// findAppendTarget returns the name of the slice being built via append inside
// the range body, if the pattern  `s = append(s, ...)` is found.
func findAppendTarget(rangeStmt *ast.RangeStmt) (string, bool) {
	for _, stmt := range rangeStmt.Body.List {

		assignStmt, ok := stmt.(*ast.AssignStmt)
		if !ok {
			continue
		}

		if len(assignStmt.Rhs) != 1 {
			continue
		}

		callExpr, ok := assignStmt.Rhs[0].(*ast.CallExpr)
		if !ok {
			continue
		}

		funcIdent, ok := callExpr.Fun.(*ast.Ident)
		if !ok || funcIdent.Name != "append" {
			continue
		}

		if len(callExpr.Args) == 0 {
			continue
		}

		targetSlice, ok := callExpr.Args[0].(*ast.Ident)
		if !ok {
			continue
		}

		if len(assignStmt.Lhs) != 1 {
			continue
		}

		lhsIdent, ok := assignStmt.Lhs[0].(*ast.Ident)
		if !ok || lhsIdent.Name != targetSlice.Name {
			continue
		}

		return targetSlice.Name, true
	}
	return "", false
}

// findSliceElemType resolves the element type of the named slice by looking up
// the slice ident (Args[0] of the append call) in TypesInfo.Uses.
// Using Uses[ident] on the slice variable avoids TypeOf on composite literals,
// which returns fully-qualified package paths like "pkg.UserDTO".
func findSliceElemType(pass *analysis.Pass, rangeStmt *ast.RangeStmt, name string) string {
	// Omit current package prefix; use short name for external packages.
	qualifier := func(pkg *types.Package) string {
		if pkg == pass.Pkg {
			return ""
		}
		return pkg.Name()
	}

	for _, stmt := range rangeStmt.Body.List {
		assign, ok := stmt.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			continue
		}
		lhs, ok := assign.Lhs[0].(*ast.Ident)
		if !ok || lhs.Name != name {
			continue
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			continue
		}
		// Args[0] is the slice ident passed to append.
		// Uses[ident] gives the declared *types.Var with the correct package.
		sliceIdent, ok := call.Args[0].(*ast.Ident)
		if !ok {
			continue
		}
		obj, ok := pass.TypesInfo.Uses[sliceIdent]
		if !ok {
			continue
		}
		sl, ok := obj.Type().(*types.Slice)
		if !ok {
			continue
		}
		return types.TypeString(sl.Elem(), qualifier)
	}

	return "interface{}"
}

// isAlreadyPreallocated returns true when the statement immediately before the
// range loop already initialises targetName as make([]T, 0, len(iterStr)) or
// make([]T, len(iterStr)), meaning no fix is needed.
func isAlreadyPreallocated(stmts []ast.Stmt, rangeIdx int, targetName, iterStr string) bool {
	if rangeIdx == 0 {
		return false
	}
	prev := stmts[rangeIdx-1]

	var callExpr *ast.CallExpr

	// var result []T = make(...) or result := make(...)
	switch s := prev.(type) {
	case *ast.AssignStmt:
		if len(s.Lhs) != 1 || len(s.Rhs) != 1 {
			return false
		}
		id, ok := s.Lhs[0].(*ast.Ident)
		if !ok || id.Name != targetName {
			return false
		}
		callExpr, _ = s.Rhs[0].(*ast.CallExpr)
	case *ast.DeclStmt:
		genDecl, ok := s.Decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.VAR {
			return false
		}
		for _, spec := range genDecl.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for j, id := range vs.Names {
				if id.Name == targetName && j < len(vs.Values) {
					callExpr, _ = vs.Values[j].(*ast.CallExpr)
				}
			}
		}
	}

	if callExpr == nil {
		return false
	}

	// Must be a call to make(...)
	makeIdent, ok := callExpr.Fun.(*ast.Ident)
	if !ok || makeIdent.Name != "make" {
		return false
	}

	// make([]T, len(x))       — 2 args, capacity is Args[1]
	// make([]T, 0, len(x))    — 3 args, capacity is Args[2]
	var capArg ast.Expr
	switch len(callExpr.Args) {
	case 2:
		capArg = callExpr.Args[1]
	case 3:
		capArg = callExpr.Args[2]
	default:
		return false
	}

	// capArg must be len(<iterStr>)
	capCall, ok := capArg.(*ast.CallExpr)
	if !ok {
		return false
	}
	lenIdent, ok := capCall.Fun.(*ast.Ident)
	if !ok || lenIdent.Name != "len" {
		return false
	}
	if len(capCall.Args) != 1 {
		return false
	}
	return exprString(capCall.Args[0]) == iterStr
}

// buildFix constructs a SuggestedFix.
//
// Strategy:
//  1. If the statement immediately before the range loop is a declaration /
//     short assignment that initialises `targetName` to a nil/empty slice, replace
//     that statement with one that uses make(...).
//  2. Otherwise, insert a `targetName = make(...)` assignment just before the
//     range loop (assumes the variable is already declared somewhere above).
func buildFix(
	pass *analysis.Pass,
	stmts []ast.Stmt,
	rangeIdx int,
	targetName, makeExpr string,
	rangePos token.Pos,
) analysis.SuggestedFix {

	fset := pass.Fset

	if rangeIdx > 0 {
		prev := stmts[rangeIdx-1]

		// Case 1a: `var targetName []T`  or  `var targetName []T = nil`
		if decl, ok := prev.(*ast.DeclStmt); ok {
			if genDecl, ok := decl.Decl.(*ast.GenDecl); ok && genDecl.Tok == token.VAR {
				for _, spec := range genDecl.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, id := range vs.Names {
						if id.Name == targetName {
							// Replace whole var decl with `targetName := make(...)`
							start := fset.File(prev.Pos()).Offset(prev.Pos())
							end := fset.File(prev.End()).Offset(prev.End())
							newText := fmt.Sprintf("%s := %s", targetName, makeExpr)
							return analysis.SuggestedFix{
								Message: fmt.Sprintf("preallocate '%s'", targetName),
								TextEdits: []analysis.TextEdit{{
									Pos:     token.Pos(fset.File(prev.Pos()).Base() + start),
									End:     token.Pos(fset.File(prev.End()).Base() + end),
									NewText: []byte(newText),
								}},
							}
						}
					}
				}
			}
		}

		// Case 1b: `targetName := []T{}`  or  `targetName := make([]T, ...)`
		if assign, ok := prev.(*ast.AssignStmt); ok && assign.Tok == token.DEFINE {
			if len(assign.Lhs) == 1 {
				if id, ok := assign.Lhs[0].(*ast.Ident); ok && id.Name == targetName {
					start := fset.File(prev.Pos()).Offset(prev.Pos())
					end := fset.File(prev.End()).Offset(prev.End())
					newText := fmt.Sprintf("%s := %s", targetName, makeExpr)
					return analysis.SuggestedFix{
						Message: fmt.Sprintf("preallocate '%s'", targetName),
						TextEdits: []analysis.TextEdit{{
							Pos:     token.Pos(fset.File(prev.Pos()).Base() + start),
							End:     token.Pos(fset.File(prev.End()).Base() + end),
							NewText: []byte(newText),
						}},
					}
				}
			}
		}
	}

	// Case 2: insert `targetName = make(...)` just before the range loop.
	insertPos := rangePos
	newText := fmt.Sprintf("%s = %s\n", targetName, makeExpr)
	return analysis.SuggestedFix{
		Message: fmt.Sprintf("preallocate '%s'", targetName),
		TextEdits: []analysis.TextEdit{{
			Pos:     insertPos,
			End:     insertPos, // zero-width insert
			NewText: []byte(newText),
		}},
	}
}

func exprString(expr ast.Expr) string {
	var buf bytes.Buffer
	_ = printer.Fprint(&buf, token.NewFileSet(), expr)
	return buf.String()
}
