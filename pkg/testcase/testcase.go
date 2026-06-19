package testcase

// Provides functionality for storing and analyzing test cases extracted from Go source files.
// The fields of the structs defined in this package should never be modified directly.

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/dave/dst"
	"github.com/dave/dst/decorator"
	"github.com/maxgreen01/go-test-analyzer/pkg/asttools"
)

// Represents an individual test case defined at the top level of a Go source file.
type TestCase struct {
	// High-level identifiers
	TestName    string // the name of the test case itself
	PackageName string // the name of the package where the test case is defined, as it appears in the source code
	FilePath    string // the path to the file where the test case is defined
	ProjectName string // the name of the overarching project that the test case is part of

	// Raw syntax data
	funcDecl *dst.FuncDecl      // the DST definition of the test case function itself
	file     *dst.File          // the DST file where the test case is defined
	pkgInfo  *decorator.Package // the underlying information about the test's package, including DST data, types, etc.
}

// Create a new TestCase struct for storage and analysis
// todo return error value more clearly either by returning nil or an error type
func CreateTestCase(funcDecl *dst.FuncDecl, file *dst.File, pkg *decorator.Package, project string) TestCase {
	if funcDecl == nil || file == nil || pkg == nil {
		slog.Error("Cannot create TestCase with nil syntax data", "funcDecl", funcDecl, "file", file, "pkg", pkg, "project", project)
		return TestCase{}
	}

	// Create the TestCase itself
	return TestCase{
		TestName:    funcDecl.Name.Name,
		PackageName: file.Name.Name, // todo CLEANUP this should probably be pkg.PkgPath for extra precision
		FilePath:    pkg.Fset.Position(pkg.Decorator.Ast.Nodes[file].(*ast.File).FileStart).Filename,
		ProjectName: project,

		funcDecl: funcDecl,
		file:     file,
		pkgInfo:  pkg,
	}
}

// Determine if the given function declaration is a valid test case.
// Returns two booleans: `valid` indicating whether this is a valid test case, and
// `badFormat` indicating whether the test case has an incorrect (but acceptable) format.
// `badFormat` is false if the function is not valid.
//
// The test case is validated using the following criteria:
// - The function name starts with "Test" followed by a capital letter
// - The function has `*testing.T` as its only formal parameter
// - The function does not have any receiver (i.e., it is not a method)
// - The function does not have any generic type parameters
// - The function does not return any values
func IsValidTestCase(funcDecl *dst.FuncDecl) (valid bool, badFormat bool) {
	if funcDecl == nil || funcDecl.Name == nil {
		return false, false
	}
	name := funcDecl.Name.Name

	// make sure the function name starts with "Test"
	// todo MAYBE allow this (and condition below) to accept "Fuzz" or "Benchmark" and indicate a different category somehow (maybe using enum `type` in TestCase)
	if !strings.HasPrefix(name, "Test") {
		// slog.Debug("\tfunction name does not start with 'Test'", "name", name)
		return false, false
	}

	// the function's 5th letter *should* be capitalized, but it's not strictly required
	if len(name) < 5 || (name[4] < 'A' || name[4] > 'Z') {
		// slog.Debug("\tfunction has bad format", "name", name)
		badFormat = true
	}

	funcType := funcDecl.Type

	// make sure the function has no receiver, type parameters, or return value
	if funcDecl.Recv != nil || funcType.TypeParams != nil || funcType.Results != nil {
		// todo maaaaaaaaybe allow this case with badFormat? print out how many times this case occurs and see if it's worth supporting
		// slog.Debug("\tfunction has bad signature", "name", name)
		return false, false
	}

	// make sure the function has exactly one parameter
	if len(funcType.Params.List) != 1 {
		// slog.Debug("\tfunction has wrong param count", "name", name)
		return false, false
	}
	paramType := funcType.Params.List[0].Type

	// safely extract all components of the parameter type, expecting something like `*testing.T`
	paramStarExpr, ok := paramType.(*dst.StarExpr)
	if !ok {
		// slog.Debug("\tfunction has non-pointer param type", "name", name, "paramType", reflect.TypeOf(paramType))
		return false, false
	}
	paramSelectorExpr, ok := paramStarExpr.X.(*dst.SelectorExpr)
	if !ok {
		// slog.Debug("\tfunction has non-selector param type", "name", name, "paramType", reflect.TypeOf(paramStarExpr.X))
		return false, false
	}
	paramSelectorIdent, ok := paramSelectorExpr.X.(*dst.Ident)
	if !ok {
		// slog.Debug("\tfunction has non-ident selector field type", "name", name, "paramType", reflect.TypeOf(paramSelectorExpr.X))
		return false, false
	}

	// check that the parameter type is exactly `*testing.T`
	// TODO allow this to accept other param types for Fuzz/Benchmark tests (and maybe testing.TB)
	// TODO maybe allow this case with `badFormat`?
	if paramSelectorIdent.Name != "testing" || paramSelectorExpr.Sel.Name != "T" {
		// slog.Debug("\tfunction has incorrect param type", "name", name, "selectorExpr", paramSelectorExpr, "field", paramSelectorIdent)
		return false, false
	}

	slog.Debug("Found valid test case:", "name", name)

	return true, badFormat
}

//
// ========== Field Getters ==========
//

// Get the the FileSet used for parsing the test's entire project
func (tc *TestCase) FileSet() *token.FileSet {
	if tc.pkgInfo == nil {
		return nil
	}
	return tc.pkgInfo.Fset
}

// Get the type information for the test case's package
func (tc *TestCase) TypeInfo() *types.Info {
	if tc.pkgInfo == nil {
		return nil
	}
	return tc.pkgInfo.TypesInfo
}

// Get all the AST files involved in the test case's package
func (tc *TestCase) GetPackageFiles() []*ast.File {
	if tc.pkgInfo == nil {
		return nil
	}
	return tc.pkgInfo.Package.Syntax
}

// Get the entire import path of the test case's package, as used when importing the package,
// e.g. "github.com/user/repo/pkg/config"
func (tc *TestCase) GetImportPath() string {
	if tc.pkgInfo == nil {
		return ""
	}
	return tc.pkgInfo.PkgPath
}

// Get the "repository root path" part of the test case's package import path.
// This is the part of the import path before the third slash, e.g. "github.com/user/repo"
// Note: this is somewhat brittle, and might not work well for all package paths.
func (tc *TestCase) GetImportPathRoot() string {
	if tc.pkgInfo == nil {
		return ""
	}
	importPath := tc.pkgInfo.PkgPath
	// Find the position of the third slash, and return everything before it
	slashCount := 0
	for i, c := range importPath {
		if c == '/' {
			slashCount++
			if slashCount == 3 {
				return importPath[:i]
			}
		}
	}
	// If there are fewer than 3 slashes, return the whole import path
	return importPath
}

// Get the DST function declaration for the test case
func (tc *TestCase) GetFuncDecl() *dst.FuncDecl { return tc.funcDecl }

// Return the list of statements in this test case
func (tc *TestCase) GetStatements() []dst.Stmt {
	if tc.funcDecl == nil || tc.funcDecl.Body == nil {
		slog.Error("Cannot get statements from test case because funcDecl or its body is nil", "testCase", tc)
		return nil
	}
	return tc.funcDecl.Body.List
}

// Return the number of statements in the test case
func (tc *TestCase) NumStatements() int {
	return len(tc.GetStatements())
}

// Return the number of individual lines (not statements) that the test case spans,
// or 0 if the number of lines cannot be determined.
func (tc *TestCase) NumLines() int {
	fset := tc.FileSet()
	if tc.funcDecl == nil || fset == nil {
		slog.Error("Cannot determine number of lines in test case because FuncDecl or FileSet is nil", "testCase", tc)
		return 0
	}
	astFunc := tc.DstToAst(tc.funcDecl)
	start := fset.Position(astFunc.Pos())
	end := fset.Position(astFunc.End())
	return end.Line - start.Line + 1
}

// Get the DST file where the test case is defined
func (tc *TestCase) GetFile() *dst.File { return tc.file }

// Get the container for all raw information about the test case's package
func (tc *TestCase) GetPackageInfo() *decorator.Package { return tc.pkgInfo }

//
// ========== Action Methods ==========
//

// Convenience method for getting the type of an expression (including identifiers) within the current TestCase's project.
// Returns `nil` if the type information for the project is not available, or if the expression is not found.
func (tc *TestCase) TypeOf(expr dst.Expr) types.Type {
	typeInfo := tc.TypeInfo()
	astExpr := tc.DstToAst(expr).(ast.Expr)
	if typeInfo == nil || astExpr == nil {
		return nil
	}
	return typeInfo.TypeOf(astExpr)
}

// Convenience method for getting the Object corresponding to an identifier within the current TestCase's project.
// Returns `nil` if the type information for the project is not available, or if the identifier is not found.
func (tc *TestCase) ObjectOf(ident *dst.Ident) types.Object {
	typeInfo := tc.TypeInfo()
	astIdent := tc.DstToAst(ident).(*ast.Ident)
	if typeInfo == nil || astIdent == nil {
		return nil
	}
	return typeInfo.ObjectOf(astIdent)
}

// Convenience method for getting the Scope corresponding to the TestCase's function.
// Returns `nil` if the type information for the project is not available.
func (tc *TestCase) FunctionScope() *types.Scope {
	typeInfo := tc.TypeInfo()
	astNode := tc.DstToAst(tc.funcDecl.Type) // See https://pkg.go.dev/go/types#Info for information about the mapping between nodes and scopes.
	if typeInfo == nil || astNode == nil {
		return nil
	}
	return typeInfo.Scopes[astNode]
}

// Returns the position and package of the definition corresponding to the given identifier,
// and whether the package matches the current test case's package.
func (tc *TestCase) GetIdentDefinition(ident *dst.Ident) (token.Pos, *types.Package, bool, error) {
	// Get the type object corresponding to the identifier (i.e. its definition)
	obj := tc.ObjectOf(ident)
	if obj == nil {
		return token.NoPos, nil, false, fmt.Errorf("could not resolve identifier %q", ident.Name)
	}
	pos := obj.Pos()
	pkg := obj.Pkg()

	// Check if the package matches the current test case's package
	isSamePackage := true
	if pkg == nil || pos == token.NoPos {
		// Universe-scope function
		// slog.Debug("Found universe-scope function", "identifier", ident.Name)
		isSamePackage = false
	} else if pkg.Path() != tc.GetImportPath() {
		// Function defined outside the current package
		// slog.Debug("Found function defined outside the current package", "identifier", ident.Name, "package", pkg.Path())
		isSamePackage = false
	}
	return pos, pkg, isSamePackage, nil
}

// Map an AST node to its corresponding DST (decorated) node to access better comment functionality.
func (tc *TestCase) AstToDst(astNode ast.Node) dst.Node {
	return tc.pkgInfo.Decorator.Dst.Nodes[astNode]
}

// Map a DST node to its corresponding AST node to access type information.
func (tc *TestCase) DstToAst(dstNode dst.Node) ast.Node {
	return tc.pkgInfo.Decorator.Ast.Nodes[dstNode]
}

// Obtain the start position of a DST node by mapping it to its corresponding AST node.
// Returns `token.NoPos` if the DST node does not have a corresponding AST node.
func (tc *TestCase) DstStartPos(dstNode dst.Node) token.Pos {
	astNode := tc.DstToAst(dstNode)
	if astNode == nil {
		return token.NoPos
	}
	return astNode.Pos()
}

//
// =============== Output Methods ===============
//

// Return a string representation of the TestCase for logging and debugging purposes
func (tc *TestCase) String() string {
	return fmt.Sprintf("TestCase{Name: %s, Package: %s, FilePath: %s, Project: %s}", tc.TestName, tc.PackageName, tc.FilePath, tc.ProjectName)
}

// Return the filepath where the test case's JSON representation should be saved, using the specified directory as a base if provided.
// The returned path is formatted like `<project>/<project>_<package>_<testName>.json`.
func (tc *TestCase) GetJSONFilePath(dir string) string {
	return filepath.Join(dir, tc.ProjectName, fmt.Sprintf("%s_%s_%s.json", tc.ProjectName, tc.PackageName, tc.TestName))
}

// Helper struct for Marshaling JSON
type testCaseJSON struct {
	Name        string `json:"name"`
	PackageName string `json:"package"`
	FilePath    string `json:"filePath"`
	ProjectName string `json:"project"`

	FuncDecl string `json:"funcDecl"`
	// Remaining syntax data is not marshaled
}

// Marshal a TestCase for JSON output
func (tc *TestCase) MarshalJSON() ([]byte, error) {
	return json.Marshal(testCaseJSON{
		Name:        tc.TestName,
		PackageName: tc.PackageName,
		FilePath:    tc.FilePath,
		ProjectName: tc.ProjectName,

		FuncDecl: asttools.NodeToString(tc.funcDecl),
		// Remaining syntax data is not marshaled
	})
}

// Unmarshal a TestCase from JSON
func (tc *TestCase) UnmarshalJSON(data []byte) error {
	var jsonData testCaseJSON
	if err := json.Unmarshal(data, &jsonData); err != nil {
		return err
	}

	// Try to decode DST fields
	var funcDecl *dst.FuncDecl
	expr, err := asttools.StringToNode(jsonData.FuncDecl)
	if err != nil {
		return fmt.Errorf("parsing TestCase FuncDecl from JSON: %w", err)
	} else {
		// Only check the type if the string was parsed successfully
		if decl, ok := expr.(*dst.FuncDecl); ok {
			funcDecl = decl
		} else {
			return fmt.Errorf("TestCase FuncDecl is not a valid function declaration: %q", jsonData.FuncDecl)
		}
	}

	*tc = TestCase{
		TestName:    jsonData.Name,
		PackageName: jsonData.PackageName,
		FilePath:    jsonData.FilePath,
		ProjectName: jsonData.ProjectName,

		funcDecl: funcDecl,
		// Remaining syntax data cannot be recovered
	}
	return nil
}
