package testcase

import (
	"fmt"
	"log/slog"
	"slices"
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
	ParsedStatements []*ExpandedStatement // the list of parsed and fully-expanded statements in the test case, avoiding expansion of production functions
	ImportedPackages []string             // the list of imported packages in the test case's file

	// Refactoring result;  only available after running `AttemptRefactoring()`
	RefactorResult RefactorResult // the result of refactoring the test case
	
	// TODO cleanup maybe find a cleaner way to pass around supplementary analysis fields and variables like LoopAnalysis,
	//*   and likewise for calling the analysis functions and adjusting the CSV results accordingly.
	//*   The custom result structs could also probably be replaced with `[]IfStmt` and `[]Loop` if they don't have any unique extra fields

	// Loop Analysis result;  only available if `--analyze-loops` option is set.
	// Use `omitempty` instead of `omitzero` so the field is always marshalled if the option is set.
	LoopAnalysis *LoopAnalysisResult `json:",omitempty"`

	// Conditional Analysis result;  only available if `--analyze-conditionals` option is set.
	// Use `omitempty` instead of `omitzero` so the field is always marshalled if the option is set.
	IfElseAnalysis *IfElseAnalysisResult `json:",omitempty"`

	// Control Flow Analysis result; only available if `--analyze-control-flow` option is set.
	// Use `omitzero` so the field is still marshalled if it's empty, but omitted if it's nil (option not set).
	ControlFlowStatements []ControlFlowStatement `json:",omitzero"`
}

// Extracts relevant information about a TestCase and saves the results to a new AnalysisResult instance
func Analyze(tc *TestCase, analyzeLoops bool, analyzeConditionals bool, analyzeControlFlow bool) *AnalysisResult {
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

	// ===== Additional analyses =====

	// Perform a loop analysis if requested
	if analyzeLoops {
		result.LoopAnalysis = AnalyzeLoops(tc, result.ParsedStatements)
	}
	// Perform a conditionals analysis inside the runner loop if requested
	if analyzeConditionals {
		result.IfElseAnalysis = AnalyzeConditionals(tc, result.ScenarioSet, result.ParsedStatements)
	}
	// Perform a control flow analysis inside the runner loop if requested
	if analyzeControlFlow {
		result.ControlFlowStatements = AnalyzeControlFlow(tc, result.ScenarioSet, result.ParsedStatements)
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
	headers := []string{
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
	if ar.LoopAnalysis != nil {
		// insert before "importedPackages"
		headers = slices.Insert(headers, len(headers)-1,
			"numLoops",
			"tableDrivenLoops",
		)
	}
	if ar.IfElseAnalysis != nil {
		// insert before "importedPackages"
		headers = slices.Insert(headers, len(headers)-1,
			"numConditionals",
		)
	}
	if ar.ControlFlowStatements != nil {
		// insert before "importedPackages"
		headers = slices.Insert(headers, len(headers)-1,
			"numControlFlowStatements",
		)
	}
	return headers
}

// Encode the AnalysisResult as a CSV row, returning the encoded data corresponding to the headers in `GetCSVHeaders()`.
// todo CLEANUP maybe simplify this using `gocarina/gocsv` to automatically marshal the struct to CSV
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

	row := []string{
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
	if ar.LoopAnalysis != nil {
		// insert before "importedPackages"
		row = slices.Insert(row, len(row)-1,
			strconv.Itoa(ar.LoopAnalysis.NumLoops),
			strconv.Itoa(ar.LoopAnalysis.CountTableDriven()),
		)
	}
	if ar.IfElseAnalysis != nil {
		// insert before "importedPackages"
		row = slices.Insert(row, len(row)-1,
			strconv.Itoa(ar.IfElseAnalysis.NumConditionals),
		)
	}
	if ar.ControlFlowStatements != nil {
		numStatements := 0
		if len(ar.ControlFlowStatements) > 0 {
			// output the number of control flow statements inside the runner loop, not counting the runner loop itself (which is always the first detected statement)
			numStatements = len(ar.ControlFlowStatements[0].GetNestedStmts())
		}

		// insert before "importedPackages"
		row = slices.Insert(row, len(row)-1,
			strconv.Itoa(numStatements),
		)
	}
	return row
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
