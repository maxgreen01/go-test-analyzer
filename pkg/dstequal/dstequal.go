// MIT License
//
// Copyright (c) 2017 Iskander Sharipov / Quasilyte
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

// Package dstequal provides deep equality check operations for DST (https://github.com/dave/dst) nodes.
// This is a simple find-and-replace fork of https://github.com/go-toolsmith/astequal.
package dstequal

import (
	"go/token"

	"github.com/dave/dst"
)

// Node reports whether two DST nodes are structurally (deep) equal.
//
// Nil arguments are permitted: true is returned if x and y are both nils.
//
// See also: Expr, Stmt, Decl functions.
func Node(x, y dst.Node) bool {
	return dstNodeEq(x, y)
}

// Expr reports whether two DST expressions are structurally (deep) equal.
//
// Nil arguments are permitted: true is returned if x and y are both nils.
// dst.BadExpr comparison always yields false.
func Expr(x, y dst.Expr) bool {
	return dstExprEq(x, y)
}

// Stmt reports whether two DST statements are structurally (deep) equal.
//
// Nil arguments are permitted: true is returned if x and y are both nils.
// dst.BadStmt comparison always yields false.
func Stmt(x, y dst.Stmt) bool {
	return dstStmtEq(x, y)
}

// Decl reports whether two DST declarations are structurally (deep) equal.
//
// Nil arguments are permitted: true is returned if x and y are both nils.
// dst.BadDecl comparison always yields false.
func Decl(x, y dst.Decl) bool {
	return dstDeclEq(x, y)
}

// Functions to perform deep equality checks between arbitrary DST nodes.

// Compare interface node types.
//
// Interfaces, as well as their values, can be nil.
//
// Even if DST does expect field X to be mandatory,
// nil checks are required as nodes can be constructed
// manually, or be partially invalid/incomplete.

func dstNodeEq(x, y dst.Node) bool {
	switch x := x.(type) {
	case dst.Expr:
		y, ok := y.(dst.Expr)
		return ok && dstExprEq(x, y)
	case dst.Stmt:
		y, ok := y.(dst.Stmt)
		return ok && dstStmtEq(x, y)
	case dst.Decl:
		y, ok := y.(dst.Decl)
		return ok && dstDeclEq(x, y)

	case *dst.Field:
		y, ok := y.(*dst.Field)
		return ok && dstFieldEq(x, y)
	case *dst.FieldList:
		y, ok := y.(*dst.FieldList)
		return ok && dstFieldListEq(x, y)

	default:
		return false
	}
}

func dstExprEq(x, y dst.Expr) bool {
	if x == nil || y == nil {
		return x == y
	}

	switch x := x.(type) {
	case *dst.Ident:
		y, ok := y.(*dst.Ident)
		return ok && dstIdentEq(x, y)

	case *dst.BasicLit:
		y, ok := y.(*dst.BasicLit)
		return ok && dstBasicLitEq(x, y)

	case *dst.FuncLit:
		y, ok := y.(*dst.FuncLit)
		return ok && dstFuncLitEq(x, y)

	case *dst.CompositeLit:
		y, ok := y.(*dst.CompositeLit)
		return ok && dstCompositeLitEq(x, y)

	case *dst.ParenExpr:
		y, ok := y.(*dst.ParenExpr)
		return ok && dstParenExprEq(x, y)

	case *dst.SelectorExpr:
		y, ok := y.(*dst.SelectorExpr)
		return ok && dstSelectorExprEq(x, y)

	case *dst.IndexExpr:
		y, ok := y.(*dst.IndexExpr)
		return ok && dstIndexExprEq(x, y)

	case *dst.IndexListExpr:
		y, ok := y.(*dst.IndexListExpr)
		return ok && dstIndexListExprEq(x, y)

	case *dst.SliceExpr:
		y, ok := y.(*dst.SliceExpr)
		return ok && dstSliceExprEq(x, y)

	case *dst.TypeAssertExpr:
		y, ok := y.(*dst.TypeAssertExpr)
		return ok && dstTypeAssertExprEq(x, y)

	case *dst.CallExpr:
		y, ok := y.(*dst.CallExpr)
		return ok && dstCallExprEq(x, y)

	case *dst.StarExpr:
		y, ok := y.(*dst.StarExpr)
		return ok && dstStarExprEq(x, y)

	case *dst.UnaryExpr:
		y, ok := y.(*dst.UnaryExpr)
		return ok && dstUnaryExprEq(x, y)

	case *dst.BinaryExpr:
		y, ok := y.(*dst.BinaryExpr)
		return ok && dstBinaryExprEq(x, y)

	case *dst.KeyValueExpr:
		y, ok := y.(*dst.KeyValueExpr)
		return ok && dstKeyValueExprEq(x, y)

	case *dst.ArrayType:
		y, ok := y.(*dst.ArrayType)
		return ok && dstArrayTypeEq(x, y)

	case *dst.StructType:
		y, ok := y.(*dst.StructType)
		return ok && dstStructTypeEq(x, y)

	case *dst.FuncType:
		y, ok := y.(*dst.FuncType)
		return ok && dstFuncTypeEq(x, y)

	case *dst.InterfaceType:
		y, ok := y.(*dst.InterfaceType)
		return ok && dstInterfaceTypeEq(x, y)

	case *dst.MapType:
		y, ok := y.(*dst.MapType)
		return ok && dstMapTypeEq(x, y)

	case *dst.ChanType:
		y, ok := y.(*dst.ChanType)
		return ok && dstChanTypeEq(x, y)

	case *dst.Ellipsis:
		y, ok := y.(*dst.Ellipsis)
		return ok && dstEllipsisEq(x, y)

	default:
		return false
	}
}

func dstStmtEq(x, y dst.Stmt) bool {
	if x == nil || y == nil {
		return x == y
	}

	switch x := x.(type) {
	case *dst.ExprStmt:
		y, ok := y.(*dst.ExprStmt)
		return ok && dstExprStmtEq(x, y)

	case *dst.SendStmt:
		y, ok := y.(*dst.SendStmt)
		return ok && dstSendStmtEq(x, y)

	case *dst.IncDecStmt:
		y, ok := y.(*dst.IncDecStmt)
		return ok && dstIncDecStmtEq(x, y)

	case *dst.AssignStmt:
		y, ok := y.(*dst.AssignStmt)
		return ok && dstAssignStmtEq(x, y)

	case *dst.GoStmt:
		y, ok := y.(*dst.GoStmt)
		return ok && dstGoStmtEq(x, y)

	case *dst.DeferStmt:
		y, ok := y.(*dst.DeferStmt)
		return ok && dstDeferStmtEq(x, y)

	case *dst.ReturnStmt:
		y, ok := y.(*dst.ReturnStmt)
		return ok && dstReturnStmtEq(x, y)

	case *dst.BranchStmt:
		y, ok := y.(*dst.BranchStmt)
		return ok && dstBranchStmtEq(x, y)

	case *dst.BlockStmt:
		y, ok := y.(*dst.BlockStmt)
		return ok && dstBlockStmtEq(x, y)

	case *dst.IfStmt:
		y, ok := y.(*dst.IfStmt)
		return ok && dstIfStmtEq(x, y)

	case *dst.CaseClause:
		y, ok := y.(*dst.CaseClause)
		return ok && dstCaseClauseEq(x, y)

	case *dst.SwitchStmt:
		y, ok := y.(*dst.SwitchStmt)
		return ok && dstSwitchStmtEq(x, y)

	case *dst.TypeSwitchStmt:
		y, ok := y.(*dst.TypeSwitchStmt)
		return ok && dstTypeSwitchStmtEq(x, y)

	case *dst.CommClause:
		y, ok := y.(*dst.CommClause)
		return ok && dstCommClauseEq(x, y)

	case *dst.SelectStmt:
		y, ok := y.(*dst.SelectStmt)
		return ok && dstSelectStmtEq(x, y)

	case *dst.ForStmt:
		y, ok := y.(*dst.ForStmt)
		return ok && dstForStmtEq(x, y)

	case *dst.RangeStmt:
		y, ok := y.(*dst.RangeStmt)
		return ok && dstRangeStmtEq(x, y)

	case *dst.DeclStmt:
		y, ok := y.(*dst.DeclStmt)
		return ok && dstDeclStmtEq(x, y)

	case *dst.LabeledStmt:
		y, ok := y.(*dst.LabeledStmt)
		return ok && dstLabeledStmtEq(x, y)

	case *dst.EmptyStmt:
		y, ok := y.(*dst.EmptyStmt)
		return ok && dstEmptyStmtEq(x, y)

	default:
		return false
	}
}

func dstDeclEq(x, y dst.Decl) bool {
	if x == nil || y == nil {
		return x == y
	}

	switch x := x.(type) {
	case *dst.GenDecl:
		y, ok := y.(*dst.GenDecl)
		return ok && dstGenDeclEq(x, y)

	case *dst.FuncDecl:
		y, ok := y.(*dst.FuncDecl)
		return ok && dstFuncDeclEq(x, y)

	default:
		return false
	}
}

// Compare concrete nodes for equality.
//
// Any node of pointer type permitted to be nil,
// hence nil checks are mandatory.

func dstIdentEq(x, y *dst.Ident) bool {
	if x == nil || y == nil {
		return x == y
	}
	return x.Name == y.Name
}

func dstKeyValueExprEq(x, y *dst.KeyValueExpr) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstExprEq(x.Key, y.Key) && dstExprEq(x.Value, y.Value)
}

func dstArrayTypeEq(x, y *dst.ArrayType) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstExprEq(x.Len, y.Len) && dstExprEq(x.Elt, y.Elt)
}

func dstStructTypeEq(x, y *dst.StructType) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstFieldListEq(x.Fields, y.Fields)
}

func dstFuncTypeEq(x, y *dst.FuncType) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstFieldListEq(x.Params, y.Params) &&
		dstFieldListEq(x.Results, y.Results) &&
		dstFieldListEq(forFuncType(x), forFuncType(y))
}

func dstBasicLitEq(x, y *dst.BasicLit) bool {
	if x == nil || y == nil {
		return x == y
	}
	return x.Kind == y.Kind && x.Value == y.Value
}

func dstBlockStmtEq(x, y *dst.BlockStmt) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstStmtSliceEq(x.List, y.List)
}

func dstFieldEq(x, y *dst.Field) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstIdentSliceEq(x.Names, y.Names) &&
		dstExprEq(x.Type, y.Type)
}

func dstFuncLitEq(x, y *dst.FuncLit) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstFuncTypeEq(x.Type, y.Type) &&
		dstBlockStmtEq(x.Body, y.Body)
}

func dstCompositeLitEq(x, y *dst.CompositeLit) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstExprEq(x.Type, y.Type) &&
		dstExprSliceEq(x.Elts, y.Elts)
}

func dstSelectorExprEq(x, y *dst.SelectorExpr) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstExprEq(x.X, y.X) && dstIdentEq(x.Sel, y.Sel)
}

func dstIndexExprEq(x, y *dst.IndexExpr) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstExprEq(x.X, y.X) && dstExprEq(x.Index, y.Index)
}

func dstIndexListExprEq(x, y *dst.IndexListExpr) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstExprEq(x.X, y.X) && dstExprSliceEq(x.Indices, y.Indices)
}

func dstSliceExprEq(x, y *dst.SliceExpr) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstExprEq(x.X, y.X) &&
		dstExprEq(x.Low, y.Low) &&
		dstExprEq(x.High, y.High) &&
		dstExprEq(x.Max, y.Max)
}

func dstTypeAssertExprEq(x, y *dst.TypeAssertExpr) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstExprEq(x.X, y.X) && dstExprEq(x.Type, y.Type)
}

func dstInterfaceTypeEq(x, y *dst.InterfaceType) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstFieldListEq(x.Methods, y.Methods)
}

func dstMapTypeEq(x, y *dst.MapType) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstExprEq(x.Key, y.Key) && dstExprEq(x.Value, y.Value)
}

func dstChanTypeEq(x, y *dst.ChanType) bool {
	if x == nil || y == nil {
		return x == y
	}
	return x.Dir == y.Dir && dstExprEq(x.Value, y.Value)
}

func dstCallExprEq(x, y *dst.CallExpr) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstExprEq(x.Fun, y.Fun) &&
		dstExprSliceEq(x.Args, y.Args) &&
		x.Ellipsis == y.Ellipsis
}

func dstEllipsisEq(x, y *dst.Ellipsis) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstExprEq(x.Elt, y.Elt)
}

func dstUnaryExprEq(x, y *dst.UnaryExpr) bool {
	if x == nil || y == nil {
		return x == y
	}
	return x.Op == y.Op && dstExprEq(x.X, y.X)
}

func dstBinaryExprEq(x, y *dst.BinaryExpr) bool {
	if x == nil || y == nil {
		return x == y
	}
	return x.Op == y.Op &&
		dstExprEq(x.X, y.X) &&
		dstExprEq(x.Y, y.Y)
}

func dstParenExprEq(x, y *dst.ParenExpr) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstExprEq(x.X, y.X)
}

func dstStarExprEq(x, y *dst.StarExpr) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstExprEq(x.X, y.X)
}

func dstFieldListEq(x, y *dst.FieldList) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstFieldSliceEq(x.List, y.List)
}

func dstEmptyStmtEq(x, y *dst.EmptyStmt) bool {
	if x == nil || y == nil {
		return x == y
	}
	return x.Implicit == y.Implicit
}

func dstLabeledStmtEq(x, y *dst.LabeledStmt) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstIdentEq(x.Label, y.Label) && dstStmtEq(x.Stmt, y.Stmt)
}

func dstExprStmtEq(x, y *dst.ExprStmt) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstExprEq(x.X, y.X)
}

func dstSendStmtEq(x, y *dst.SendStmt) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstExprEq(x.Chan, y.Chan) && dstExprEq(x.Value, y.Value)
}

func dstDeclStmtEq(x, y *dst.DeclStmt) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstDeclEq(x.Decl, y.Decl)
}

func dstIncDecStmtEq(x, y *dst.IncDecStmt) bool {
	if x == nil || y == nil {
		return x == y
	}
	return x.Tok == y.Tok && dstExprEq(x.X, y.X)
}

func dstAssignStmtEq(x, y *dst.AssignStmt) bool {
	if x == nil || y == nil {
		return x == y
	}
	return x.Tok == y.Tok &&
		dstExprSliceEq(x.Lhs, y.Lhs) &&
		dstExprSliceEq(x.Rhs, y.Rhs)
}

func dstGoStmtEq(x, y *dst.GoStmt) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstCallExprEq(x.Call, y.Call)
}

func dstDeferStmtEq(x, y *dst.DeferStmt) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstCallExprEq(x.Call, y.Call)
}

func dstReturnStmtEq(x, y *dst.ReturnStmt) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstExprSliceEq(x.Results, y.Results)
}

func dstBranchStmtEq(x, y *dst.BranchStmt) bool {
	if x == nil || y == nil {
		return x == y
	}
	return x.Tok == y.Tok && dstIdentEq(x.Label, y.Label)
}

func dstIfStmtEq(x, y *dst.IfStmt) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstStmtEq(x.Init, y.Init) &&
		dstExprEq(x.Cond, y.Cond) &&
		dstBlockStmtEq(x.Body, y.Body) &&
		dstStmtEq(x.Else, y.Else)
}

func dstCaseClauseEq(x, y *dst.CaseClause) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstExprSliceEq(x.List, y.List) &&
		dstStmtSliceEq(x.Body, y.Body)
}

func dstSwitchStmtEq(x, y *dst.SwitchStmt) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstStmtEq(x.Init, y.Init) &&
		dstExprEq(x.Tag, y.Tag) &&
		dstBlockStmtEq(x.Body, y.Body)
}

func dstTypeSwitchStmtEq(x, y *dst.TypeSwitchStmt) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstStmtEq(x.Init, y.Init) &&
		dstStmtEq(x.Assign, y.Assign) &&
		dstBlockStmtEq(x.Body, y.Body)
}

func dstCommClauseEq(x, y *dst.CommClause) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstStmtEq(x.Comm, y.Comm) && dstStmtSliceEq(x.Body, y.Body)
}

func dstSelectStmtEq(x, y *dst.SelectStmt) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstBlockStmtEq(x.Body, y.Body)
}

func dstForStmtEq(x, y *dst.ForStmt) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstStmtEq(x.Init, y.Init) &&
		dstExprEq(x.Cond, y.Cond) &&
		dstStmtEq(x.Post, y.Post) &&
		dstBlockStmtEq(x.Body, y.Body)
}

func dstRangeStmtEq(x, y *dst.RangeStmt) bool {
	if x == nil || y == nil {
		return x == y
	}
	return x.Tok == y.Tok &&
		dstExprEq(x.Key, y.Key) &&
		dstExprEq(x.Value, y.Value) &&
		dstExprEq(x.X, y.X) &&
		dstBlockStmtEq(x.Body, y.Body)
}

func dstFuncDeclEq(x, y *dst.FuncDecl) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstFieldListEq(x.Recv, y.Recv) &&
		dstIdentEq(x.Name, y.Name) &&
		dstFuncTypeEq(x.Type, y.Type) &&
		dstBlockStmtEq(x.Body, y.Body)
}

func dstGenDeclEq(x, y *dst.GenDecl) bool {
	if x == nil || y == nil {
		return x == y
	}

	if x.Tok != y.Tok {
		return false
	}
	if len(x.Specs) != len(y.Specs) {
		return false
	}

	switch x.Tok {
	case token.IMPORT:
		for i := range x.Specs {
			xspec := x.Specs[i].(*dst.ImportSpec)
			yspec := y.Specs[i].(*dst.ImportSpec)
			if !dstImportSpecEq(xspec, yspec) {
				return false
			}
		}
	case token.TYPE:
		for i := range x.Specs {
			xspec := x.Specs[i].(*dst.TypeSpec)
			yspec := y.Specs[i].(*dst.TypeSpec)
			if !dstTypeSpecEq(xspec, yspec) {
				return false
			}
		}
	default:
		for i := range x.Specs {
			xspec := x.Specs[i].(*dst.ValueSpec)
			yspec := y.Specs[i].(*dst.ValueSpec)
			if !dstValueSpecEq(xspec, yspec) {
				return false
			}
		}
	}

	return true
}

func dstImportSpecEq(x, y *dst.ImportSpec) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstIdentEq(x.Name, y.Name) && dstBasicLitEq(x.Path, y.Path)
}

func dstTypeSpecEq(x, y *dst.TypeSpec) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstIdentEq(x.Name, y.Name) && dstExprEq(x.Type, y.Type) &&
		dstFieldListEq(forTypeSpec(x), forTypeSpec(y))
}

func dstValueSpecEq(x, y *dst.ValueSpec) bool {
	if x == nil || y == nil {
		return x == y
	}
	return dstIdentSliceEq(x.Names, y.Names) &&
		dstExprEq(x.Type, y.Type) &&
		dstExprSliceEq(x.Values, y.Values)
}

// Compare slices for equality.
//
// Each slice element that has pointer type permitted to be nil,
// hence instead of using adhoc comparison of values,
// equality functions that are defined above are used.

func dstIdentSliceEq(xs, ys []*dst.Ident) bool {
	if len(xs) != len(ys) {
		return false
	}
	for i := range xs {
		if !dstIdentEq(xs[i], ys[i]) {
			return false
		}
	}
	return true
}

func dstFieldSliceEq(xs, ys []*dst.Field) bool {
	if len(xs) != len(ys) {
		return false
	}
	for i := range xs {
		if !dstFieldEq(xs[i], ys[i]) {
			return false
		}
	}
	return true
}

func dstStmtSliceEq(xs, ys []dst.Stmt) bool {
	if len(xs) != len(ys) {
		return false
	}
	for i := range xs {
		if !dstStmtEq(xs[i], ys[i]) {
			return false
		}
	}
	return true
}

func dstExprSliceEq(xs, ys []dst.Expr) bool {
	if len(xs) != len(ys) {
		return false
	}
	for i := range xs {
		if !dstExprEq(xs[i], ys[i]) {
			return false
		}
	}
	return true
}

// forTypeSpec returns n.TypeParams.
func forTypeSpec(n *dst.TypeSpec) *dst.FieldList {
	if n == nil {
		return nil
	}
	return n.TypeParams
}

// forFuncType returns n.TypeParams.
func forFuncType(n *dst.FuncType) *dst.FieldList {
	if n == nil {
		return nil
	}
	return n.TypeParams
}
