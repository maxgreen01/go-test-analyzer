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
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/maxgreen01/go-test-analyzer/pkg/asttools"
	"golang.org/x/tools/go/packages"
)

// Represents an individual test case defined at the top level of a Go source file.
type TestCase struct {
	// High-level identifiers
	TestName    string // the name of the test case itself
	PackageName string // the name of the package where the test case is defined, as it appears in the source code
	FilePath    string // the path to the file where the test case is defined
	ProjectName string // the name of the overarching project that the test case is part of

	// Raw syntax data
	funcDecl *ast.FuncDecl     // the AST definition of the test case function itself
	file     *ast.File         // the AST file where the test case is defined
	pkgInfo  *packages.Package // the actual AST information about the test's package, including AST data, types, etc.
}

// Create a new TestCase struct for storage and analysis
// todo return error value more clearly either by returning nil or an error type
func CreateTestCase(funcDecl *ast.FuncDecl, file *ast.File, pkg *packages.Package, project string) TestCase {
	if funcDecl == nil || file == nil || pkg == nil {
		slog.Error("Cannot create TestCase with nil syntax data", "funcDecl", funcDecl, "file", file, "pkg", pkg, "project", project)
		return TestCase{}
	}

	// Create the TestCase itself
	return TestCase{
		TestName:    funcDecl.Name.Name,
		PackageName: file.Name.Name, // todo CLEANUP this should probably be pkg.PkgPath for extra precision
		FilePath:    pkg.Fset.Position(file.FileStart).Filename,
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
func IsValidTestCase(funcDecl *ast.FuncDecl) (valid bool, badFormat bool) {
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
	starExpr, ok := paramType.(*ast.StarExpr)
	if !ok {
		// slog.Debug("\tfunction has non-pointer param type", "name", name, "paramType", reflect.TypeOf(paramType))
		return false, false
	}
	selectorExpr, ok := starExpr.X.(*ast.SelectorExpr)
	if !ok {
		// slog.Debug("\tfunction has non-selector param type", "name", name, "paramType", reflect.TypeOf(starExpr.X))
		return false, false
	}
	paramPackageIdent, ok := selectorExpr.X.(*ast.Ident)
	if !ok {
		// slog.Debug("\tfunction has non-ident param package", "name", name, "paramType", reflect.TypeOf(selectorExpr.X))
		return false, false
	}

	// check that the parameter type is exactly `*testing.T`
	// TODO allow this to accept other param types for Fuzz/Benchmark tests (and maybe testing.TB)
	// TODO maybe allow this case with `badFormat`?
	if paramPackageIdent.Name != "testing" || selectorExpr.Sel.Name != "T" {
		// slog.Debug("\tfunction has invalid param type", "name", name, "paramType", reflect.TypeOf(paramType))
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
	return tc.pkgInfo.Syntax
}

// Get the entire import path of the test case's package
func (tc *TestCase) GetImportPath() string {
	if tc.pkgInfo == nil {
		return ""
	}
	return tc.pkgInfo.PkgPath
}

// Get the "repository root path" part of the test case's package import path.
// This is the part of the import path before the third slash, e.g. "github.com/user/repo"
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

// Get the AST function declaration for the test case
func (tc *TestCase) GetFuncDecl() *ast.FuncDecl { return tc.funcDecl }

// Return the list of statements in this test case
func (tc *TestCase) GetStatements() []ast.Stmt {
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
	start := fset.Position(tc.funcDecl.Pos())
	end := fset.Position(tc.funcDecl.End())
	return end.Line - start.Line + 1
}

// Get the AST file where the test case is defined
func (tc *TestCase) GetFile() *ast.File { return tc.file }

// Get the container for all raw information about the test case's package
func (tc *TestCase) GetPackageInfo() *packages.Package { return tc.pkgInfo }

//
// ========== Action Methods ==========
//

// Convenience method for getting the type of an expression (including identifiers) within the current TestCase's project.
// Returns `nil` if the type information for the project is not available, or if the expression is not found.
func (tc *TestCase) TypeOf(expr ast.Expr) types.Type {
	typeInfo := tc.TypeInfo()
	if typeInfo == nil || expr == nil {
		return nil
	}
	return typeInfo.TypeOf(expr)
}

// Convenience method for getting the Object corresponding to an identifier within the current TestCase's project.
// Returns `nil` if the type information for the project is not available, or if the identifier is not found.
func (tc *TestCase) ObjectOf(ident *ast.Ident) types.Object {
	typeInfo := tc.TypeInfo()
	if typeInfo == nil || ident == nil {
		return nil
	}
	return typeInfo.ObjectOf(ident)
}

//
// ========== Test Execution ==========
//

// Represents the result of a test execution, as run by the `go test` command.
type TestExecutionResult int

const (
	TestExecutionResultNotRun           TestExecutionResult = iota // The test was not executed
	TestExecutionResultCompilationError                            // The test failed to compile
	TestExecutionResultSkip                                        // The test was skipped
	TestExecutionResultFail                                        // The test failed
	TestExecutionResultPass                                        // The test passed successfully
)

func (ter TestExecutionResult) String() string {
	switch ter {
	case TestExecutionResultNotRun:
		return "notRun"
	case TestExecutionResultCompilationError:
		return "compilationError"
	case TestExecutionResultSkip:
		return "skip"
	case TestExecutionResultFail:
		return "fail"
	case TestExecutionResultPass:
		return "pass"
	default:
		return "unknown"
	}
}

func (ter TestExecutionResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(ter.String())
}

func (ter *TestExecutionResult) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}
	switch strings.ToLower(str) {
	case "notRun":
		*ter = TestExecutionResultNotRun
	case "compilationError":
		*ter = TestExecutionResultCompilationError
	case "skip":
		*ter = TestExecutionResultSkip
	case "fail":
		*ter = TestExecutionResultFail
	case "pass":
		*ter = TestExecutionResultPass
	default:
		slog.Warn("Unknown test execution result", "result", str)
		*ter = TestExecutionResultNotRun
	}
	return nil
}

// Execute a test based on the contents of its corresponding file in the file system using `go test`, and return the results.
// Returns an error if the test fails for any reason.
func (tc *TestCase) Execute() (TestExecutionResult, error) {
	if tc.FilePath == "" || tc.TestName == "" {
		return TestExecutionResultNotRun, fmt.Errorf("missing FilePath or TestName in TestCase: %v", tc)
	}

	slog.Debug("Executing test case", "file", tc.FilePath, "test", tc)

	// Build the go test command
	testPattern := fmt.Sprintf("^%s$", tc.TestName)
	cmd := []string{"go", "test", "-run", testPattern, "-v"}
	c := exec.Command(cmd[0], cmd[1:]...)
	c.Dir = filepath.Dir(tc.FilePath) // Use the directory of the test file as the working directory

	// Execute the command and save the output
	// FIXME maybe make this concurrent somehow?
	outBytes, err := c.Output()
	output := string(outBytes)

	if err != nil {
		// Extract any relevant information from stderr
		var stderr string = "[no stderr output]"
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			stderr = string(ee.Stderr)
		}

		// Check for compilation error
		if strings.Contains(output, "[build failed]") {
			return TestExecutionResultCompilationError, fmt.Errorf("compilation error: %s", stderr)
		}
		// Check for test failure
		if strings.Contains(output, "--- FAIL") {
			return TestExecutionResultFail, fmt.Errorf("test failed: %s", stderr)
		}
		// Unknown error
		return TestExecutionResultFail, fmt.Errorf("unknown error: %s", stderr)
	}

	// Check for invalid test name
	if strings.Contains(output, "no tests to run") {
		return TestExecutionResultNotRun, fmt.Errorf("no tests to run for pattern %q in file %q", testPattern, tc.FilePath)
	}

	// Check for test being skipped deliberately
	if strings.Contains(output, "--- SKIP") {
		return TestExecutionResultSkip, nil
	}

	// Check for test pass
	// This is basically the default option, but we check the output string anyway
	if strings.Contains(output, "--- PASS") {
		return TestExecutionResultPass, nil
	}

	// Fallback: unknown result
	return TestExecutionResultFail, fmt.Errorf("unknown test result: %s", output)
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

		FuncDecl: asttools.NodeToString(tc.funcDecl, tc.FileSet()),
		// Remaining syntax data is not marshaled
	})
}

// Unmarshal a TestCase from JSON
func (tc *TestCase) UnmarshalJSON(data []byte) error {
	var jsonData testCaseJSON
	if err := json.Unmarshal(data, &jsonData); err != nil {
		return err
	}

	// Try to decode AST fields
	var funcDecl *ast.FuncDecl
	expr, err := asttools.StringToNode(jsonData.FuncDecl)
	if err != nil {
		return fmt.Errorf("parsing TestCase FuncDecl from JSON: %w", err)
	} else {
		// Only check the type if the string was parsed successfully
		if decl, ok := expr.(*ast.FuncDecl); ok {
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
