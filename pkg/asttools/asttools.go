// A collection of general-purpose utility functions for working with AST and DST nodes

package asttools

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"log/slog"
	"os"
	"reflect"
	"strings"

	"github.com/dave/dst"
	"github.com/dave/dst/decorator"
	"github.com/maxgreen01/go-test-analyzer/pkg/dstequal"
	"golang.org/x/tools/go/ast/astutil"
)

//
// ========== Conversion Functions ==========
//

// Converts a DST node to a string representation, or returns an empty string if formatting fails
// todo CLEANUP should return actual errors
func NodeToString(node dst.Node) string {
	if node == nil || reflect.ValueOf(node).IsNil() {
		return ""
	}

	// Marker decorations to identify the node being processed if it has to be wrapped before printing
	const startMarker = "/* <<<NODE_START>>> */"
	const endMarker = "/* <<<NODE_END>>> */"

	// If the node is already a file, it can be printed directly without extra steps
	dstFile, isFile := node.(*dst.File)

	// If the node is NOT a file, format the DST data by wrapping it in a fake file
	if !isFile {
		// Clone before modifying to avoid modifying the original DST node
		node = dst.Clone(node)

		// Add markers to the DST node so we can extract the relevant portion of the formatted string later
		node.Decorations().Start.Prepend("\n") // ensure the actual node is on its own line to keep tabs consistent
		node.Decorations().Start.Prepend(startMarker)
		node.Decorations().End.Append(endMarker)

		// Place the node inside a fake file depending on its type
		var fakeDecl dst.Decl
		switch n := node.(type) {
		case dst.Decl:
			// Put the decl directly into the file
			fakeDecl = n

		case dst.Stmt:
			// Put the statement inside a fake function
			fakeDecl = &dst.FuncDecl{
				Name: dst.NewIdent("_"),
				Type: &dst.FuncType{
					Params: &dst.FieldList{},
				},
				Body: &dst.BlockStmt{
					List: []dst.Stmt{n},
				},
			}

		case dst.Expr:
			// Hard case: put the expression inside a function as the RHS of an assignment.
			// If it's a CompositeLit or KeyValueExpr, wrap it in `FakeType{}` to avoid parser issues.
			switch n.(type) {
			case *dst.CompositeLit, *dst.KeyValueExpr:
				wrapper := &dst.CompositeLit{
					Type: dst.NewIdent("_"),
					Elts: []dst.Expr{n},
				}
				n = wrapper
			}

			fakeDecl = &dst.FuncDecl{
				Name: dst.NewIdent("_"),
				Type: &dst.FuncType{
					Params: &dst.FieldList{},
				},
				Body: &dst.BlockStmt{
					List: []dst.Stmt{
						&dst.AssignStmt{
							Lhs: []dst.Expr{dst.NewIdent("_")},
							Tok: token.DEFINE,
							Rhs: []dst.Expr{n},
						},
					},
				},
			}
		default:
			slog.Error("Cannot format unsupported DST node type", "nodeType", fmt.Sprintf("%T", node))
			return ""
		}

		dstFile = &dst.File{
			Name:  dst.NewIdent("_"),
			Decls: []dst.Decl{fakeDecl},
		}
	}

	// Restore the DST file to an AST file
	restorer := decorator.NewRestorer()
	restoredFile, err := restorer.RestoreFile(dstFile)
	if err != nil {
		slog.Error("Failed to restore AST node", "err", err, "nodeType", fmt.Sprintf("%T", node))
		return ""
	}

	var buf bytes.Buffer
	if err := format.Node(&buf, restorer.Fset, restoredFile); err != nil {
		// Note: could try restoring again using an import resolver, but this adds complexity and could rename tests incorrectly
		slog.Error("Failed to format AST node", "err", err, "nodeType", fmt.Sprintf("%T", node))
		slog.Debug("formatting failure", "astDump", ast.Fprint(&buf, restorer.Fset, restoredFile, nil))
		return ""
	}
	printed := buf.Bytes()

	// If the node is a file, we can return the entire formatted string without worrying about extracting the relevant portion
	if isFile {
		return string(printed)
	}

	// Extract the relevant portion of the formatted string between the markers
	startIndex := bytes.Index(printed, []byte(startMarker))
	endIndex := bytes.LastIndex(printed, []byte(endMarker))
	if startIndex == -1 || endIndex == -1 || startIndex >= endIndex {
		slog.Error("Failed to extract relevant portion of formatted AST node", "nodeType", fmt.Sprintf("%T", node), "startIndex", startIndex, "endIndex", endIndex, "printed", string(printed))
		return ""
	}
	extracted := printed[startIndex+len(startMarker) : endIndex]

	// Strip leading tabs from all lines, then remove any remaining extra whitespace
	return strings.TrimSpace(normalizeTabs(string(extracted)))
}

// Remove any leading tabs that are present in all lines of a multi-line string, e.g. the lingering artifacts of the fake function wrapper
func normalizeTabs(s string) string {
	lines := strings.Split(s, "\n")
	// Keep track of the number of tabs that are present in all non-empty lines
	minTabs := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue // Ignore empty lines
		}
		n := len(line) - len(strings.TrimLeft(line, "\t")) // Count leading tabs
		if minTabs == -1 || n < minTabs {
			minTabs = n
		}
	}
	if minTabs <= 0 {
		return s
	}
	prefix := strings.Repeat("\t", minTabs)
	for i, line := range lines {
		lines[i] = strings.TrimPrefix(line, prefix)
	}
	return strings.Join(lines, "\n")
}

// Fake package and function declarations are used when parsing strings into DST nodes
const (
	_fakePackage = "package _"
	_fakeFunc    = "func _() "
)

// Parses a string (usually from JSON) into the corresponding DST expression.
// This function tries to parse the string as a declaration, statement, or expression in that order.
// TODO DST - might need to use an existing decorator here to avoid discarding the fset
func StringToNode(str string) (dst.Node, error) {
	// First try parsing the string as a declaration by treating the string as a Go source file
	fakeFset := token.NewFileSet()
	fileStr := _fakePackage + "\n" + str
	file, err := decorator.ParseFile(fakeFset, "", fileStr, parser.ParseComments)
	if err == nil {
		// Extract and return the first declaration in the file
		if len(file.Decls) > 0 {
			return file.Decls[0], nil
		}
		slog.Debug("Parsed fake file has no declarations; now trying to parse as statement or expression", "input", str)
	}

	// Try parsing the string as a statement by wrapping the string in a function
	funcStr := _fakePackage + "\n" + _fakeFunc + "{\n" + str + "\n}"
	file, err = decorator.ParseFile(fakeFset, "", funcStr, parser.ParseComments)
	if err == nil {
		// Extract and return the first statement in the function body
		if len(file.Decls) > 0 {
			if funcDecl, ok := file.Decls[0].(*dst.FuncDecl); ok && len(funcDecl.Body.List) > 0 {
				return funcDecl.Body.List[0], nil
			}
			slog.Debug("Parsed fake function has no statements; now trying to parse as expression", "file", funcStr)
		} else {
			// This should never happen
			slog.Debug("Parsed fake file (with fake function) has no declarations; now trying to parse as expression", "input", str)
		}
	}

	// Try parsing the original string as an expression
	expr, err := parser.ParseExpr(str)
	if err != nil {
		return nil, fmt.Errorf("parsing node string %q: %w", str, err)
	}
	dstNode, err := decorator.Decorate(fakeFset, expr)
	if err != nil {
		return nil, fmt.Errorf("converting node string to DST %q: %w", str, err)
	}

	// The string is a valid expression
	return dstNode, nil
}

//
// ========== Node Detection, Retrieval, and Modification Functions ==========
//

// Returns a boolean indicating whether a statement is a function call expression of the form `owner.name(...)`,
// as well as a reference to the `dst.CallExpr` if the statement matches.
func IsSelectorFuncCall(stmt dst.Stmt, owner, name string) (bool, *dst.CallExpr) {
	if exprStmt, ok := stmt.(*dst.ExprStmt); ok {
		if callExpr, ok := exprStmt.X.(*dst.CallExpr); ok {
			if MatchSelectorExpr(callExpr.Fun, owner, name) {
				return true, callExpr
			}
		}
	}
	return false, nil
}

// Returns a boolean indicating whether a selector expression has the form `owner.name`.
func MatchSelectorExpr(expr dst.Expr, owner, name string) bool {
	if selExpr, ok := expr.(*dst.SelectorExpr); ok {
		if ident, ok := selExpr.X.(*dst.Ident); ok && ident.Name == owner && selExpr.Sel.Name == name {
			return true
		}
	}
	return false
}

// Returns the AST file containing the given position in the provided package files.
// Returns nil if none of the provided files contain the position.
func GetEnclosingFile(pos token.Pos, packageFiles []*ast.File) *ast.File {
	for _, file := range packageFiles {
		if file.FileStart <= pos && pos <= file.FileEnd {
			return file
		}
	}
	return nil
}

// Returns the AST function declaration (and corresponding file) enclosing the given position in the provided package files.
// Returns nil if no function declaration is found, or if none of the provided files contain the position.
func GetEnclosingFunction(pos token.Pos, packageFiles []*ast.File) (*ast.FuncDecl, *ast.File) {
	file := GetEnclosingFile(pos, packageFiles)
	if file == nil {
		return nil, nil
	}
	path, _ := astutil.PathEnclosingInterval(file, pos, pos)
	for i := len(path) - 1; i >= 0; i-- {
		// Iterate backward to find the highest-level function declaration first
		if fn, ok := path[i].(*ast.FuncDecl); ok {
			return fn, file
		}
	}
	return nil, nil
}

// Replace the reference to the `old` FuncDecl in its parent file with a reference to the `new` FuncDecl,
// without modifying either of the FuncDecls themselves. The function must be a top-level declaration in the file.
// Note that the contents of the functions are not compared, only their names.
// Returns an error if the replacement was not successful.
func ReplaceFuncDecl(old, new *dst.FuncDecl, file *dst.File) error {
	if file == nil {
		return fmt.Errorf("cannot replace function declaration in nil package")
	}
	if old == nil {
		return fmt.Errorf("cannot replace nil function declaration in package %s", file.Name.Name)
	}
	if new == nil {
		return fmt.Errorf("cannot replace function declaration with nil in package %s", file.Name.Name)
	}

	for i, decl := range file.Decls {
		// Match function declarations by name so their contents don't have to match
		if fn, ok := decl.(*dst.FuncDecl); ok && fn.Name.Name == old.Name.Name {
			// Replace the reference to the old function declaration with the new one
			file.Decls[i] = new
			return nil
		}
	}

	return fmt.Errorf("could not find function declaration %q in package %s", old.Name.Name, file.Name.Name)
}

// Returns the index of the given statement within a function body, or an error if the statement is not found.
// The contents of the statement (but not necessarily its underlying pointers) must exactly match a statement in the provided body.
func FindStmtInBody(stmt dst.Stmt, body []dst.Stmt) (int, error) {
	if stmt == nil {
		return -1, fmt.Errorf("cannot find nil stmt in function body")
	}
	for i, s := range body {
		// Deep compare based on contents
		if dstequal.Stmt(stmt, s) {
			return i, nil
		}
	}
	return -1, fmt.Errorf("could not find stmt in function body")
}

// Returns the i-th statement in the new body, where i is the index of the provided statement within its own parent body.
// For example, if the given statement is at index 2 in its parent body, this returns the statement at index 2 in the new body.
func GetStmtWithSameIndex(stmt dst.Stmt, parentBody, newBody []dst.Stmt) (dst.Stmt, error) {
	index, err := FindStmtInBody(stmt, parentBody)
	if err != nil {
		return nil, fmt.Errorf("finding statement in parent body: %w", err)
	}
	if index < 0 || index >= len(newBody) {
		return nil, fmt.Errorf("statement index %d out of bounds for new body containing %d statements", index, len(newBody))
	}
	return newBody[index], nil
}

// Returns the name of the first detected parameter in the function declaration that exactly matches any of the
// provided parameter types. If no matching parameter is found, returns an error.
func GetParamNameByType(funcDecl *dst.FuncDecl, paramTypes ...dst.Expr) (string, error) {
	if funcDecl == nil || funcDecl.Type == nil {
		return "", fmt.Errorf("cannot detect parameter name for uninitialized function declaration")
	}
	if len(paramTypes) == 0 {
		return "", fmt.Errorf("cannot detect parameter name without parameter types")
	}
	// Iterate the function parameters by type
	for _, param := range funcDecl.Type.Params.List {
		if param.Type == nil {
			continue
		}
		// Check for any of the provided parameter types
		for _, paramType := range paramTypes {
			if !dstequal.Expr(param.Type, paramType) {
				continue
			}
			if len(param.Names) == 0 {
				slog.Debug("Found parameter with matching type, but it has no name")
				continue
			}
			return param.Names[0].Name, nil
		}
	}
	return "", fmt.Errorf("could not find parameter name with types %+#v in function %q", paramTypes, funcDecl.Name.Name)
}

//
// ========== Node Creation Functions ==========
//

// Creates a DST selector expression of the form `owner.name`.
func NewSelectorExpr(owner, name string) dst.Expr {
	return &dst.SelectorExpr{
		X:   dst.NewIdent(owner),
		Sel: dst.NewIdent(name),
	}
}

// Creates a DST call expression statement using the provided function and arguments.
func NewCallExprStmt(fun dst.Expr, args []dst.Expr) *dst.ExprStmt {
	return &dst.ExprStmt{
		X: &dst.CallExpr{
			Fun:  fun,
			Args: args,
		},
	}
}

//
// ========== Output Functions ==========
//

// Saves the contents of the specified DST file to the disk using the specified path,
// overwriting any existing data at the specified path.
func SaveFileContents(path string, newFile *dst.File) error {
	if newFile == nil {
		return fmt.Errorf("cannot replace file contents with nil DST file")
	}

	// Format the new DST data
	formattedStr := NodeToString(newFile)
	if formattedStr == "" {
		return fmt.Errorf("formatting new file contents for %q", path)
	}

	// Write to the file
	if err := os.WriteFile(path, []byte(formattedStr), 0644); err != nil {
		return fmt.Errorf("writing new file contents to %q: %w", path, err)
	}

	slog.Debug("Successfully replaced the contents of file", "file", path)
	return nil
}

//
// ========== Type System Functions ==========
//

// Returns whether a Type is Basic and has the specified info.
// See `go/types.Basic` for more details.
func IsBasicType(typ types.Type, info types.BasicInfo) bool {
	if basic, ok := typ.(*types.Basic); ok {
		return basic.Info() == info
	}
	return false
}

// Returns T given *T or an alias of *T.
// For all other types it is the identity function.
// [copied from `go/typesinternal` package]
func Unpointer(t types.Type) types.Type {
	if t == nil {
		return nil
	}
	if ptr, ok := t.Underlying().(*types.Pointer); ok {
		return ptr.Elem()
	}
	return t
}

// Returns whether a Type's underlying Type is a pointer.
func IsPointer(t types.Type) bool {
	if t == nil {
		return false
	}
	_, ok := t.Underlying().(*types.Pointer)
	return ok
}

// Returns the underlying type of a type, unwrapping any pointer types.
func UnderlyingType(t types.Type) types.Type {
	if t == nil {
		return nil
	}
	return Unpointer(t).Underlying()
}

// Generates a unique name for a variable in the given scope by appending a number to the base name until it is unique.
// For example, if the base name is "x" and a variable named "x" is already defined in the scope, the next attempt will
// be "x1", then "x2", and so on. If the base name is not already defined, it will be returned without modification.
func GenerateUniqueName(scope *types.Scope, base string) string {
	name := base
	for i := 1; IsNameUsed(scope, name); i++ {
		name = fmt.Sprintf("%s%d", base, i)
	}
	return name
}

// Returns whether a variable with the given name is used in the given scope,
// any of the scope's parents (upward), or any nested scopes (downward).
// This indicates whether the name is at risk of being redeclared or shadowed.
func IsNameUsed(scope *types.Scope, name string) bool {
	if name == "" || scope == nil {
		return false
	}

	// Check the current scope and its parents
	if _, obj := scope.LookupParent(name, token.NoPos); obj != nil {
		return true
	}

	// Recursively check all child scopes (nested blocks, loops, ifs, etc.)
	return isNameUsedInChildScopes(scope, name)
}

// Returns whether a name is used in any of the child scopes of the given scope,
// checked recursively.
func isNameUsedInChildScopes(scope *types.Scope, name string) bool {
	for child := range scope.Children() {
		if child.Lookup(name) != nil {
			return true
		}
		if isNameUsedInChildScopes(child, name) {
			return true
		}
	}
	return false
}
