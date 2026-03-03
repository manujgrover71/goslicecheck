package main

import (
	"bytes"
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

		ast.Inspect(file, func(n ast.Node) bool {

			// Look for range loops
			rangeStmt, ok := n.(*ast.RangeStmt)
			if !ok {
				return true
			}

			// Get iterated expression (range over X)
			iterExpr := rangeStmt.X

			// Check if iterated type is slice
			iterType := pass.TypesInfo.TypeOf(iterExpr)
			if iterType == nil {
				return true
			}

			_, isSlice := iterType.(*types.Slice)
			if !isSlice {
				return true
			}

			// Look inside loop body
			for _, stmt := range rangeStmt.Body.List {

				assignStmt, ok := stmt.(*ast.AssignStmt)
				if !ok {
					continue
				}

				// Check: result = append(result, something)
				if len(assignStmt.Rhs) != 1 {
					continue
				}

				callExpr, ok := assignStmt.Rhs[0].(*ast.CallExpr)
				if !ok {
					continue
				}

				// Is it append(...)?
				funcIdent, ok := callExpr.Fun.(*ast.Ident)
				if !ok || funcIdent.Name != "append" {
					continue
				}

				if len(callExpr.Args) == 0 {
					continue
				}

				// append target slice
				targetSlice, ok := callExpr.Args[0].(*ast.Ident)
				if !ok {
					continue
				}

				// assignment LHS should match target slice
				if len(assignStmt.Lhs) != 1 {
					continue
				}

				lhsIdent, ok := assignStmt.Lhs[0].(*ast.Ident)
				if !ok || lhsIdent.Name != targetSlice.Name {
					continue
				}

				// At this point we found:
				// result = append(result, ...)

				pass.Reportf(
					rangeStmt.Pos(),
					"slice '%s' can be preallocated with capacity len(%s)",
					targetSlice.Name,
					exprString(iterExpr),
				)
			}

			return true
		})
	}

	return nil, nil
}

func exprString(expr ast.Expr) string {
	var buf bytes.Buffer
	_ = printer.Fprint(&buf, token.NewFileSet(), expr)
	return buf.String()
}
