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

type loopDirection int

const (
	loopForward loopDirection = iota
	loopReverse
)

var Analyzer = &analysis.Analyzer{
	Name: "goslicecheck",
	Doc:  "detects slices that can be preallocated",
	Run:  run,
}

func run(pass *analysis.Pass) (interface{}, error) {

	for _, file := range pass.Files {

		ast.Inspect(file, func(n ast.Node) bool {

			block, ok := n.(*ast.BlockStmt)
			if !ok {
				return true
			}

			for i, stmt := range block.List {

				var iterExpr ast.Expr

				switch s := stmt.(type) {
				case *ast.RangeStmt:
					// --- existing range loop handling ---
					iterType := pass.TypesInfo.TypeOf(s.X)
					if iterType == nil {
						continue
					}

					// Accept slices and maps — both have deterministic len().
					// Using underlying to handle named slice/map types like `type MySlice []T`.
					switch iterType.Underlying().(type) {
					case *types.Slice, *types.Map:
						// ok
					default:
						continue
					}

					iterExpr = s.X

					targetName, found := findAppendTarget(s.Body)
					if !found {
						continue
					}

					iterStr := exprString(iterExpr)
					if isAlreadyPreallocated(block.List, i, targetName, iterStr) {
						continue
					}

					elemType := findSliceElemType(pass, s.Body, targetName)
					makeExpr := fmt.Sprintf("make([]%s, 0, len(%s))", elemType, iterStr)
					fix := buildFix(pass, block.List, i, targetName, makeExpr, s.Pos())

					pass.Report(analysis.Diagnostic{
						Pos:            s.Pos(),
						Message:        fmt.Sprintf("slice '%s' can be preallocated with capacity len(%s)", targetName, iterStr),
						SuggestedFixes: []analysis.SuggestedFix{fix},
					})

				case *ast.ForStmt:
					// --- new: traditional for loop ---
					sliceName, loopKind, ok := extractForLoopSlice(s)
					if !ok {
						continue
					}

					// For forward loops the slice appears in the condition (i < len(s)),
					// for reverse loops it appears in the init (i := len(s)).
					var searchExpr ast.Expr
					if loopKind == loopForward {
						searchExpr = s.Cond
					} else {
						searchExpr = s.Init.(*ast.AssignStmt).Rhs[0] // len(s) or len(s)-1
					}

					// Resolve the slice ident to confirm it's actually a slice type.
					sliceIdent := findIdentInExpr(searchExpr, sliceName)
					if sliceIdent == nil {
						continue
					}

					iterType := pass.TypesInfo.TypeOf(sliceIdent)
					if iterType == nil {
						continue
					}
					if _, isSlice := iterType.(*types.Slice); !isSlice {
						continue
					}

					iterExpr = sliceIdent

					targetName, found := findAppendTarget(s.Body)
					if !found {
						continue
					}

					iterStr := exprString(iterExpr)
					if isAlreadyPreallocated(block.List, i, targetName, iterStr) {
						continue
					}

					elemType := findSliceElemType(pass, s.Body, targetName)
					makeExpr := fmt.Sprintf("make([]%s, 0, len(%s))", elemType, iterStr)
					fix := buildFix(pass, block.List, i, targetName, makeExpr, s.Pos())

					pass.Report(analysis.Diagnostic{
						Pos:            s.Pos(),
						Message:        fmt.Sprintf("slice '%s' can be preallocated with capacity len(%s)", targetName, iterStr),
						SuggestedFixes: []analysis.SuggestedFix{fix},
					})
				}
			}

			return true
		})
	}

	return nil, nil
}

// findAppendTarget returns the name of the slice being built via append inside
// the range body, if the pattern  `s = append(s, ...)` is found.
func findAppendTarget(body *ast.BlockStmt) (string, bool) {
	for _, stmt := range body.List {

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
func findSliceElemType(pass *analysis.Pass, body *ast.BlockStmt, name string) string {
	qualifier := func(pkg *types.Package) string {
		if pkg == pass.Pkg {
			return ""
		}
		return pkg.Name()
	}

	for _, stmt := range body.List {
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

		// Args[0] is the slice being appended to — skip it, we want Args[1+]
		// to determine what element type is being collected.
		if len(call.Args) < 2 {
			continue
		}

		appendedArg := call.Args[1]

		// If the appended value is a plain ident, resolve via Uses.
		// This handles both slice elem vars and map k/v vars correctly.
		if argIdent, ok := appendedArg.(*ast.Ident); ok {
			if obj, ok := pass.TypesInfo.Uses[argIdent]; ok {
				return types.TypeString(obj.Type(), qualifier)
			}
		}

		// Fallback: derive type from TypesInfo.Types for composite expressions.
		if tv, ok := pass.TypesInfo.Types[appendedArg]; ok {
			return types.TypeString(tv.Type, qualifier)
		}
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

// extractForLoopSlice inspects a traditional for loop's condition and returns
// the slice/array name + its len-expression if the loop has a canonical form
// whose iteration count is determined by len(x):
//
//	Forward:  for i := 0; i < len(s); i++
//	          for i := 0; i <= len(s)-1; i++
//	          for i := 0; len(s) > i; i++
//
//	Reverse:  for i := len(s) - 1; i >= 0; i--
//	          for i := len(s);     i > 0;  i--
//	          for i := len(s);     i >= 1; i--
func extractForLoopSlice(forStmt *ast.ForStmt) (sliceName string, direction loopDirection, ok bool) {
	if forStmt.Init == nil || forStmt.Cond == nil || forStmt.Post == nil {
		return "", 0, false
	}

	initAssign, ok := forStmt.Init.(*ast.AssignStmt)
	if !ok || initAssign.Tok != token.DEFINE ||
		len(initAssign.Lhs) != 1 || len(initAssign.Rhs) != 1 {
		return "", 0, false
	}
	indexIdent, ok := initAssign.Lhs[0].(*ast.Ident)
	if !ok {
		return "", 0, false
	}

	if isIntLit(initAssign.Rhs[0], "0") && isIncrement(forStmt.Post, indexIdent.Name) {
		name, ok := extractLenArgFromUpperBound(forStmt.Cond, indexIdent.Name)
		return name, loopForward, ok
	}

	if isDecrement(forStmt.Post, indexIdent.Name) {
		name, ok := extractLenArgFromInit(initAssign.Rhs[0])
		if !ok {
			return "", 0, false
		}
		if !reverseCondIsValid(forStmt.Cond, indexIdent.Name) {
			return "", 0, false
		}
		return name, loopReverse, true
	}

	return "", 0, false
}

// extractLenArgFromUpperBound handles the condition side of forward loops.
// Accepts:  i < len(s)   |   len(s) > i   |   i <= len(s)-1   |   len(s)-1 >= i
func extractLenArgFromUpperBound(cond ast.Expr, indexName string) (string, bool) {
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok {
		return "", false
	}

	switch bin.Op {
	case token.LSS: // i < len(s)
		if !isIdent(bin.X, indexName) {
			return "", false
		}
		return lenCallArg(bin.Y)

	case token.GTR: // len(s) > i
		if !isIdent(bin.Y, indexName) {
			return "", false
		}
		return lenCallArg(bin.X)

	case token.LEQ: // i <= len(s)-1
		if !isIdent(bin.X, indexName) {
			return "", false
		}
		return lenCallArgMinusOne(bin.Y)

	case token.GEQ: // len(s)-1 >= i
		if !isIdent(bin.Y, indexName) {
			return "", false
		}
		return lenCallArgMinusOne(bin.X)
	}

	return "", false
}

// extractLenArgFromInit handles the init RHS of reverse loops.
// Accepts:  len(s)   |   len(s) - 1
func extractLenArgFromInit(expr ast.Expr) (string, bool) {
	// Plain len(s)
	if name, ok := lenCallArg(expr); ok {
		return name, true
	}
	// len(s) - 1
	return lenCallArgMinusOne(expr)
}

// reverseCondIsValid checks that the condition of a reverse loop compares the
// index variable against a small non-negative constant (0 or 1), ensuring the
// loop count is still fully determined by len(s).
//
// Accepts:  i >= 0  |  i > 0  |  i >= 1  |  0 <= i  |  0 < i  |  1 <= i
func reverseCondIsValid(cond ast.Expr, indexName string) bool {
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok {
		return false
	}

	switch bin.Op {
	case token.GEQ, token.GTR: // i >= 0 | i > 0 | i >= 1
		if !isIdent(bin.X, indexName) {
			return false
		}
		lit, ok := bin.Y.(*ast.BasicLit)
		return ok && lit.Kind == token.INT && (lit.Value == "0" || lit.Value == "1")

	case token.LEQ, token.LSS: // 0 <= i | 1 <= i | 0 < i
		if !isIdent(bin.Y, indexName) {
			return false
		}
		lit, ok := bin.X.(*ast.BasicLit)
		return ok && lit.Kind == token.INT && (lit.Value == "0" || lit.Value == "1")
	}

	return false
}

// lenCallArg returns the single ident argument of a bare len(...) call.
func lenCallArg(expr ast.Expr) (string, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	fn, ok := call.Fun.(*ast.Ident)
	if !ok || fn.Name != "len" || len(call.Args) != 1 {
		return "", false
	}
	arg, ok := call.Args[0].(*ast.Ident)
	if !ok {
		return "", false
	}
	return arg.Name, true
}

// lenCallArgMinusOne matches `len(s) - 1` and returns the slice name.
func lenCallArgMinusOne(expr ast.Expr) (string, bool) {
	bin, ok := expr.(*ast.BinaryExpr)
	if !ok || bin.Op != token.SUB {
		return "", false
	}
	if !isIntLit(bin.Y, "1") {
		return "", false
	}
	return lenCallArg(bin.X)
}

// isIntLit returns true if expr is an integer literal with the given value.
func isIntLit(expr ast.Expr, value string) bool {
	lit, ok := expr.(*ast.BasicLit)
	return ok && lit.Kind == token.INT && lit.Value == value
}

// isIncrement returns true for `name++` or `name += 1`.
func isIncrement(post ast.Stmt, name string) bool {
	switch s := post.(type) {
	case *ast.IncDecStmt:
		id, ok := s.X.(*ast.Ident)
		return ok && s.Tok == token.INC && id.Name == name
	case *ast.AssignStmt:
		if s.Tok != token.ADD_ASSIGN || len(s.Lhs) != 1 || len(s.Rhs) != 1 {
			return false
		}
		id, ok := s.Lhs[0].(*ast.Ident)
		if !ok || id.Name != name {
			return false
		}
		lit, ok := s.Rhs[0].(*ast.BasicLit)
		return ok && lit.Kind == token.INT && lit.Value == "1"
	}
	return false
}

// isDecrement returns true for `name--` or `name -= 1`.
func isDecrement(post ast.Stmt, name string) bool {
	switch s := post.(type) {
	case *ast.IncDecStmt:
		id, ok := s.X.(*ast.Ident)
		return ok && s.Tok == token.DEC && id.Name == name
	case *ast.AssignStmt:
		if s.Tok != token.SUB_ASSIGN || len(s.Lhs) != 1 || len(s.Rhs) != 1 {
			return false
		}
		id, ok := s.Lhs[0].(*ast.Ident)
		if !ok || id.Name != name {
			return false
		}
		lit, ok := s.Rhs[0].(*ast.BasicLit)
		return ok && lit.Kind == token.INT && lit.Value == "1"
	}
	return false
}

// isIdent returns true if expr is an *ast.Ident with the given name.
func isIdent(expr ast.Expr, name string) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == name
}

// findIdentInExpr does a shallow walk of expr to find the first *ast.Ident
// whose Name matches target. Used to obtain a typed node for type-checking.
func findIdentInExpr(expr ast.Expr, target string) *ast.Ident {
	var found *ast.Ident
	ast.Inspect(expr, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		if id, ok := n.(*ast.Ident); ok && id.Name == target {
			found = id
			return false
		}
		return true
	})
	return found
}
