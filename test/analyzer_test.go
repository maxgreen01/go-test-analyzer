package test

// This file defines the actual black-box tests of the analyzer's functionality.

import (
	"testing"

	"github.com/maxgreen01/go-test-analyzer/internal/parsercommands"
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
