package asttools

// A collection of general-purpose AST-related utility functions

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"log/slog"
	"os"
	"reflect"

	"github.com/go-toolsmith/astequal"
	"golang.org/x/tools/go/ast/astutil"
)

//
// ========== Conversion Functions ==========
//

// Defines a printer config for converting AST nodes to string representations
var printerCfg = &printer.Config{
	Mode:     printer.UseSpaces | printer.TabIndent,
	Tabwidth: 4,
}

// Converts an AST node to a string representation using `go/printer`, or return an error string if formatting fails
// todo CLEANUP should return actual errors
func NodeToString(node ast.Node, fset *token.FileSet) string {
	if node == nil || reflect.ValueOf(node).IsNil() {
		return ""
	}
	if fset == nil {
		slog.Error("Failed to format AST node because FileSet is nil", "nodeType", fmt.Sprintf("%T", node))
		return ""
	}

	var buf bytes.Buffer
	err := printerCfg.Fprint(&buf, fset, node)
	if err != nil {
		slog.Error("Failed to format AST node", "error", err, "nodeType", fmt.Sprintf("%T", node))
		return ""
	}
	return buf.String()
}

// Fake package and function declarations are used when parsing strings into AST nodes
const (
	_fakePackage = "package _"
	_fakeFunc    = "func _() "
)

// Parses a string (usually from JSON) into the corresponding AST expression.
// This function tries to parse the string as a declaration, statement, or expression in that order.
func StringToNode(str string) (ast.Node, error) {
	// First try parsing the string as a declaration by treating the string as a Go source file
	fakeFset := token.NewFileSet()
	fileStr := _fakePackage + "\n" + str
	file, err := parser.ParseFile(fakeFset, "", fileStr, parser.ParseComments)
	if err == nil {
		// Extract and return the first declaration in the file
		if len(file.Decls) > 0 {
			return file.Decls[0], nil
		}
		slog.Debug("Parsed fake file has no declarations; now trying to parse as statement or expression", "input", str)
	}

	// Try parsing the string as a statement by wrapping the string in a function
	funcStr := _fakeFunc + "\n" + _fakeFunc + "{\n" + str + "\n}"
	file, err = parser.ParseFile(fakeFset, "", funcStr, parser.ParseComments)
	if err == nil {
		// Extract and return the first statement in the function body
		if len(file.Decls) > 0 {
			if funcDecl, ok := file.Decls[0].(*ast.FuncDecl); ok && len(funcDecl.Body.List) > 0 {
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

	// The string is a valid expression
	return expr, nil
}

//
// ========== Node Detection, Retrieval, and Modification Functions ==========
//

// Returns a boolean indicating whether a statement is a function call expression of the form `owner.name(...)`,
// as well as a reference to the `ast.CallExpr` if the statement matches.
func IsSelectorFuncCall(stmt ast.Stmt, owner, name string) (bool, *ast.CallExpr) {
	if exprStmt, ok := stmt.(*ast.ExprStmt); ok {
		if callExpr, ok := exprStmt.X.(*ast.CallExpr); ok {
			if MatchSelectorExpr(callExpr.Fun, owner, name) {
				return true, callExpr
			}
		}
	}
	return false, nil
}

// Returns a boolean indicating whether a selector expression has the form `owner.name`.
func MatchSelectorExpr(expr ast.Expr, owner, name string) bool {
	if selExpr, ok := expr.(*ast.SelectorExpr); ok {
		if ident, ok := selExpr.X.(*ast.Ident); ok && ident.Name == owner && selExpr.Sel.Name == name {
			return true
		}
	}
	return false
}

// Returns the file containing the given position in the provided package files.
// Returns nil if none of the provided files contain the position.
func GetEnclosingFile(pos token.Pos, packageFiles []*ast.File) *ast.File {
	for _, file := range packageFiles {
		if file.FileStart <= pos && pos <= file.FileEnd {
			return file
		}
	}
	return nil
}

// Returns the function declaration (and corresponding file) enclosing the given position in the provided package files.
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
func ReplaceFuncDecl(old, new *ast.FuncDecl, file *ast.File) error {
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
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == old.Name.Name {
			// Replace the reference to the old function declaration with the new one
			file.Decls[i] = new
			return nil
		}
	}

	return fmt.Errorf("could not find function declaration %q in package %s", old.Name.Name, file.Name.Name)
}

// Returns the index of the given statement within a function body, or an error if the statement is not found.
// The contents of the statement (but not necessarily its underlying pointers) must exactly match a statement in the provided body.
func FindStmtInBody(stmt ast.Stmt, body []ast.Stmt) (int, error) {
	if stmt == nil {
		return -1, fmt.Errorf("cannot find nil stmt in function body")
	}
	for i, s := range body {
		// Deep compare based on contents
		if astequal.Stmt(stmt, s) {
			return i, nil
		}
	}
	return -1, fmt.Errorf("could not find stmt in function body")
}

// Returns the i-th statement in the new body, where i is the index of the provided statement within its own parent body.
// For example, if the given statement is at index 2 in its parent body, this returns the statement at index 2 in the new body.
func GetStmtWithSameIndex(stmt ast.Stmt, parentBody, newBody []ast.Stmt) (ast.Stmt, error) {
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
func GetParamNameByType(funcDecl *ast.FuncDecl, paramTypes ...ast.Expr) (string, error) {
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
			if !astequal.Expr(param.Type, paramType) {
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

// Creates a selector expression of the form `owner.name`.
func NewSelectorExpr(owner, name string) ast.Expr {
	return &ast.SelectorExpr{
		X:   ast.NewIdent(owner),
		Sel: ast.NewIdent(name),
	}
}

// Creates a call expression statement using the provided function and arguments.
func NewCallExprStmt(fun ast.Expr, args []ast.Expr) *ast.ExprStmt {
	return &ast.ExprStmt{
		X: &ast.CallExpr{
			Fun:  fun,
			Args: args,
		},
	}
}

//
// ========== Output Functions ==========
//

// Saves the contents of the specified AST file to the disk using the specified path, after
// formatting the AST data with `go/format` using the provided FileSet. Any existing file
// at the specified path will be overwritten.
func SaveFileContents(path string, newFile *ast.File, fset *token.FileSet) error {
	if newFile == nil {
		return fmt.Errorf("cannot replace file contents with nil AST file")
	}
	if fset == nil {
		return fmt.Errorf("cannot replace file contents because FileSet is nil")
	}

	// Format the new AST data
	var buffer bytes.Buffer
	if err := format.Node(&buffer, fset, newFile); err != nil {
		return fmt.Errorf("formatting new file contents %q: %w", path, err)
	}

	// Write to the file
	if err := os.WriteFile(path, buffer.Bytes(), 0644); err != nil {
		return fmt.Errorf("writing to file %q: %w", path, err)
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
	if ptr, ok := t.Underlying().(*types.Pointer); ok {
		return ptr.Elem()
	}
	return t
}
