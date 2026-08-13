package test

// This file defines the actual black-box tests of the analyzer's functionality.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/gocarina/gocsv"
	"github.com/maxgreen01/go-test-analyzer/internal/parsercommands"
	"github.com/sebdah/goldie/v2"
)

// TestAnalyzeWithLoops verifies that the regular analyzer (without refactoring) and loop analysis both work as expected.
func TestAnalyzeWithLoops(t *testing.T) {
	opts := parsercommands.AnalyzeOptions{AnalyzeLoops: true}
	results := runAnalyzer(t, opts)

	results.checkErrorLog(t)
	checkGoldenAnalyzeCSV(t, results, "analyzer-report-with-loops.csv")

	// More detailed JSON file assertions
	checkGoldenAnalyzeJSON(t, results, []testCaseAssertion{
		{
			// Check nested loop analysis
			testName:   "TestExpandedStatements",
			goldenName: "TestExpandedStatements-with-loops.json",
		},
		{
			// Check loop analysis with third-party packages
			testName:   "TestThirdPartyPackage",
			goldenName: "TestThirdPartyPackage-with-loops.json",
		},
		{
			// Check table-driven detection of secondary data structure
			testName:   "TestRangeNonStruct",
			goldenName: "TestRangeNonStruct-with-loops.json",
		},
	}, opts)
}

// TestAnalyzeWithConditionals verifies that conditional statement analysis works as expected.
func TestAnalyzeWithConditionals(t *testing.T) {
	opts := parsercommands.AnalyzeOptions{AnalyzeConditionals: true}
	results := runAnalyzer(t, opts)

	results.checkErrorLog(t)
	checkGoldenAnalyzeCSV(t, results, "analyzer-report-with-conditionals.csv")

	// More detailed JSON file assertions
	checkGoldenAnalyzeJSON(t, results, []testCaseAssertion{
		// Check tests with mixed loops and conditionals inside each other
		{
			testName:   "TestConditionals",
			goldenName: "TestConditionals-with-conditionals.json",
		},
		{
			testName:   "TestExpandedStatements",
			goldenName: "TestExpandedStatements-with-conditionals.json",
		},
		// Non-table-driven tests should have no detected control flow statements
		{
			testName:   "TestAssertionLoopOnly",
			goldenName: "TestAssertionLoopOnly-with-conditionals.json",
		},
	}, opts)
}

// TestAnalyzeWithControlFlow verifies that unified control flow analysis works as expected.
func TestAnalyzeWithControlFlow(t *testing.T) {
	opts := parsercommands.AnalyzeOptions{AnalyzeControlFlow: true}
	results := runAnalyzer(t, opts)

	results.checkErrorLog(t)
	checkGoldenAnalyzeCSV(t, results, "analyzer-report-with-control-flow.csv")

	// More detailed JSON file assertions
	checkGoldenAnalyzeJSON(t, results, []testCaseAssertion{
		// Check tests with mixed loops and conditionals inside each other
		{
			testName:   "TestConditionals",
			goldenName: "TestConditionals-with-control-flow.json",
		},
		{
			testName:   "TestExpandedStatements",
			goldenName: "TestExpandedStatements-with-control-flow.json",
		},
		// Control flow statements with function calls in the header should not inspect the statement's body nodes
		{
			testName:   "TestRangeInt",
			goldenName: "TestRangeInt-with-control-flow.json",
		},
		// Non-table-driven tests should have no detected control flow statements
		{
			testName:   "TestAssertionLoopOnly",
			goldenName: "TestAssertionLoopOnly-with-control-flow.json",
		},
	}, opts)
}

// TestRefactorSubtest verifies that subtest refactoring and keep-refactored-files work as expected.
func TestRefactorSubtest(t *testing.T) {
	opts := parsercommands.AnalyzeOptions{
		RefactorStrategy:    "subtest",
		KeepRefactoredFiles: true,
	}
	results := runAnalyzer(t, opts)

	results.checkErrorLog(t)
	checkGoldenAnalyzeCSV(t, results, "analyzer-report-refactor-subtest.csv")

	checkGoldenAnalyzeJSON(t, results, []testCaseAssertion{
		{
			// Should be "success" refactor status
			testName:   "TestNoSubtest",
			goldenName: "TestNoSubtest-subtest-refactor.json",
		},
		{
			// Should be "badFields" refactor status
			testName:   "TestMapRangeNonStruct",
			goldenName: "TestMapRangeNonStruct-subtest-refactor.json",
		},
		{
			// Should be "notRun" refactor status
			testName:   "TestNoLoops",
			goldenName: "TestNoLoops-subtest-refactor.json",
		},
	}, opts)
}

// TestRefactorSubtestDoNotKeep verifies that refactored files are properly restored when `keep-refactored-files` is false.
func TestRefactorSubtestDoNotKeep(t *testing.T) {
	opts := parsercommands.AnalyzeOptions{
		RefactorStrategy:    "subtest",
		KeepRefactoredFiles: false,
	}
	results := runAnalyzer(t, opts)

	results.checkErrorLog(t)
	checkGoldenAnalyzeJSON(t, results, []testCaseAssertion{
		{
			// Should be "success" refactor status
			testName:   "TestNoSubtest",
			goldenName: "TestNoSubtest-subtest-refactor.json",
		},
	}, opts)
}

// TestAnalyzeWithComplexity verifies that complexity calculation and ranking work as expected.
func TestAnalyzeWithComplexity(t *testing.T) {
	opts := parsercommands.AnalyzeOptions{CalculateComplexity: true, AnalyzeControlFlow: true}
	results := runAnalyzer(t, opts)

	results.checkErrorLog(t)
	checkGoldenAnalyzeJSON(t, results, []testCaseAssertion{
		// Check control flow results of complex tests
		{
			testName:   "TestTableBasedConditionals",
			goldenName: "TestTableBasedConditionals-with-complexity.json",
		},
		{
			testName:   "TestConditionalLogic",
			goldenName: "TestConditionalLogic-with-complexity.json",
		},
	}, opts)

	// Sanitize a copy of the complexity scores CSV results
	complexityFileName := fmt.Sprintf("%s-complexity-scores.csv", samplePackage)
	scoresPath := filepath.Join(results.outputDir, complexityFileName)
	f, err := os.Open(scoresPath)
	if err != nil {
		t.Fatalf("Failed to open %s: %v", complexityFileName, err)
	}
	defer f.Close()

	// !! THIS STRUCT MUST BE UPDATED WHENEVER THE CSV REPORT FORMAT CHANGES !!
	var rows []struct {
		Project                      string `csv:"project"`
		FilePath                     string `csv:"filePath"`
		ImportPath                   string `csv:"importPath"`
		Name                         string `csv:"name"`
		OverallScore                 string `csv:"overallScore"`
		NumSubtestsInConditionals    string `csv:"numSubtestsInConditionals"`
		NumFunctionFields            string `csv:"numFunctionFields"`
		PctRunnerStmtsInConditionals string `csv:"pctRunnerStmtsInConditionals"`
		MaxAssertionDepth            string `csv:"maxAssertionDepth"`
		PctTableFieldsOnlyInConditionals string `csv:"pctTableFieldsOnlyInConditionals"`
	}

	if err := gocsv.UnmarshalFile(f, &rows); err != nil {
		t.Fatalf("Failed to unmarshal %s: %v", complexityFileName, err)
	}

	// Sanitize dynamic identifying fields
	for i := range rows {
		row := &rows[i]
		row.Project = ""
		row.FilePath = ""
		row.ImportPath = ""
	}

	// Marshal the sanitized results back to CSV bytes
	var csvBuf bytes.Buffer
	if err := gocsv.Marshal(&rows, &csvBuf); err != nil {
		t.Fatalf("Failed to marshal sanitized complexity results: %v", err)
	}

	// Check that the results match the golden file
	g := goldie.New(t, goldie.WithFixtureDir(goldenDir))
	g.Assert(t, complexityFileName, csvBuf.Bytes())
}
