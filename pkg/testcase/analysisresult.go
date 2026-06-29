package testcase

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/dave/dst"
	"github.com/maxgreen01/go-test-analyzer/internal/filewriter"
)

// Represents the result of analyzing a TestCase, including information about its table-driven structure.
type AnalysisResult struct {
	// Reference to the original test case being analyzed
	TestCase *TestCase

	// Analysis data
	ScenarioSet      *ScenarioSet         // the set of scenarios defined in this test case, if it is table-driven
	ParsedStatements []*ExpandedStatement // the list of parsed and fully-expanded statements in the test case
	ImportedPackages []string             // the list of imported packages in the test case's file

	// Refactoring result - only available after running `AttemptRefactoring()`
	RefactorResult RefactorResult // the result of refactoring the test case
}

// Extracts relevant information about a TestCase and saves the results to a new AnalysisResult instance
func Analyze(tc *TestCase) *AnalysisResult {
	slog.Debug("Analyzing TestCase", "testCase", tc)

	// Initialize the AnalysisResult
	result := &AnalysisResult{
		TestCase: tc,
	}

	if tc == nil || tc.GetFuncDecl() == nil || tc.GetFile() == nil {
		slog.Error("Cannot analyze TestCase because it has nil syntax data", "testCase", tc)
		return nil
	}
	fset := tc.FileSet()
	if fset == nil {
		slog.Error("Cannot analyze TestCase because FileSet is nil", "testCase", tc)
		return nil
	}

	// Expand all the individual statements in the test case's body
	stmts := tc.GetStatements()
	result.ParsedStatements = make([]*ExpandedStatement, len(stmts))
	for i, stmt := range stmts {
		// Try to expand the statement if it's a call to a testing helper function
		result.ParsedStatements[i] = ExpandStatement(stmt, tc, true)
	}

	// Populate table-driven test data
	result.ScenarioSet = IdentifyScenarioSet(tc, result.ParsedStatements)

	// Extract imported packages from the file's DST
	var imports []*dst.ImportSpec
	if tc.GetFile() != nil {
		imports = tc.GetFile().Imports
		for _, imp := range imports {
			result.ImportedPackages = append(result.ImportedPackages, strings.Trim(imp.Path.Value, "\""))
		}
	} else {
		slog.Error("Cannot extract imported packages in TestCase because File is nil", "testCase", tc)
	}

	return result
}

// Return whether the test case is table-driven, based on the detected ScenarioSet data
func (ar *AnalysisResult) IsTableDriven() bool {
	if ar.ScenarioSet == nil {
		return false
	}
	return ar.ScenarioSet.IsTableDriven()
}

//
// ========== Output Methods ==========
//

// Return the headers for the CSV representation of the AnalysisResult.
// Complex or large fields are excluded for the sake of brevity.
func (ar *AnalysisResult) GetCSVHeaders() []string {
	return []string{
		"project",
		"filePath",
		"importPath",
		"name",
		"isTableDriven",
		"scenarioDataStructure",
		"scenarioCount",
		"scenarioNameField",
		"scenarioExpectedFields",
		"scenarioHasFunctionFields",
		"scenarioUsesSubtest",
		"refactorStrategy",
		"refactorGenerationStatus",
		"originalExecutionResult",
		"refactoredExecutionResult",
		"importedPackages",
	}
}

// Encode the AnalysisResult as a CSV row, returning the encoded data corresponding to the headers in `GetCSVHeaders()`.
func (ar *AnalysisResult) EncodeAsCSV() []string {
	// Replace nil fields with empty data to avoid nil pointer dereferences
	tc := ar.TestCase
	if tc == nil {
		tc = &TestCase{}
	}
	ss := ar.ScenarioSet
	if ss == nil {
		ss = &ScenarioSet{}
	}
	rr := ar.RefactorResult

	return []string{
		tc.ProjectName,
		tc.FilePath,
		tc.ImportPath,
		tc.TestName,
		strconv.FormatBool(ss.IsTableDriven()),
		ss.DataStructure.String(),
		strconv.Itoa(len(ss.Scenarios)),
		ss.NameField,
		strings.Join(ss.ExpectedFields, ", "),
		strconv.FormatBool(ss.HasFunctionFields),
		strconv.FormatBool(ss.UsesSubtest),
		rr.Strategy.String(),
		rr.GenerationStatus.String(),
		rr.OriginalExecutionResult.String(),
		rr.RefactoredExecutionResult.String(),
		strings.Join(ar.ImportedPackages, ", "),
	}
}

// Save the AnalysisResult as JSON to a file named like `<project>/<project>_<package>_<testName>_<hash>.json` in the specified directory
// (or the default output directory if not specified).
func (ar *AnalysisResult) SaveAsJSON(dir string) error {
	tc := ar.TestCase
	slog.Info("Saving test case analysis results as JSON", "testCase", tc)

	// Construct the filepath using information from the test case, inside the provided directory.
	// If the directory is empty, the `filewriter` will automatically prepend the output directory instead.
	path := tc.GetJSONFilePath(dir)

	// Create and write the file
	err := filewriter.WriteToFile(path, ar)
	if err != nil {
		return fmt.Errorf("saving analysis results for test case %q as JSON: %w", tc.TestName, err)
	}
	return nil
}
