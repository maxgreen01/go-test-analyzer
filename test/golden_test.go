package test

// This file contains helper functions checking the analyzer's outputs against golden files.
// Note that the `goldie` library internally handles updating the generated golden files using the `-update` flag.

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/gocarina/gocsv"
	"github.com/maxgreen01/go-test-analyzer/internal/parsercommands"
	"github.com/sebdah/goldie/v2"
)

// goldenDir is the directory where golden files are stored for this test package.
const goldenDir = "testdata/golden"

// checkGoldenAnalyzeCSV sanitizes a copy of the analyzer's CSV results, then checks it against the golden file with the provided name (without the `.golden` suffix).
// This involves clearing each row's `Project`, `FilePath`, and `ImportPath` fields since they naturally vary between test runs, and sorting rows alphabetically by test name.
func checkGoldenAnalyzeCSV(t *testing.T, results *analyzerResults, goldenName string) {
	t.Helper()

	// To make the CSV results stable and machine-independent:
	// 1. Clear dynamic identifying fields (Project, FilePath, ImportPath)
	// 2. Sort the rows alphabetically by test name
	sanitized := slices.Clone(results.rows)
	for i := range sanitized {
		row := &sanitized[i]
		row.Project = ""
		row.FilePath = ""
		row.ImportPath = ""
	}
	sort.Slice(sanitized, func(i, j int) bool {
		return sanitized[i].Name < sanitized[j].Name
	})

	// Marshal the sanitized results back to CSV bytes
	var csvBuf bytes.Buffer
	if err := gocsv.Marshal(&sanitized, &csvBuf); err != nil {
		t.Fatalf("Failed to marshal sanitized results: %v", err)
	}

	// Check that the results match the golden file
	g := goldie.New(t, goldie.WithFixtureDir(goldenDir))
	g.Assert(t, goldenName, csvBuf.Bytes())
}

// testCaseAssertion represents a single test case for which we want to check the analyzer's output against a golden file.
type testCaseAssertion struct {
	// The name of the test to check
	testName string
	// The name of the golden file to compare against (without the `.golden` suffix)
	goldenName string
}

// checkGoldenAnalyzeJSON checks the analyzer's JSON output for the specified test names against the corresponding golden files.
// This also ensures that any refactored code either does or does not exist on disk, depending on the `KeepRefactoredFiles` option.
// This involves clearing the test's `project`, `filePath`, and `importPath` fields since they naturally vary between test runs.
func checkGoldenAnalyzeJSON(t *testing.T, results *analyzerResults, cases []testCaseAssertion, opts parsercommands.AnalyzeOptions) {
	t.Helper()
	g := goldie.New(t, goldie.WithFixtureDir(goldenDir))

	for _, tc := range cases {
		t.Run(tc.testName, func(t *testing.T) {
			dataBytes := results.jsonFile(t, tc.testName)
			// TODO cleanup maybe unmarshal the JSON files directly into an AnalysisResult struct in `jsonFile()` to avoid the manual deconstructing logic,
			//   especially because this reorders the fields in the JSON output, which makes it much harder to read (especially ParsedStatements)
			var data map[string]any
			if err := json.Unmarshal(dataBytes, &data); err != nil {
				t.Fatalf("Failed to unmarshal JSON: %v", err)
			}

			// Sanitize the dynamic identifying fields
			var funcDecl string
			if tcMap, ok := data["TestCase"].(map[string]any); ok {
				tcMap["project"] = ""
				tcMap["filePath"] = ""
				tcMap["importPath"] = ""
				funcDecl = tcMap["funcDecl"].(string)
			}

			// Check whether the refactored code does or doesn't exist on disk, and sanitize the dynamic paths inside refactorings list
			if refactorResult, ok := data["RefactorResult"].(map[string]any); ok {
				if refactorings, ok := refactorResult["refactorings"].([]any); ok {
					for _, r := range refactorings {
						if refactoringMap, ok := r.(map[string]any); ok {

							filePath, _ := refactoringMap["filePath"].(string)
							refactoredCode, _ := refactoringMap["refactored"].(string)

							// Clear the filepath now that it's stored locally
							refactoringMap["filePath"] = ""

							if filePath == "" || refactoredCode == "" {
								continue
							}

							// Get the saved code on disk
							content, err := os.ReadFile(filePath)
							if err != nil {
								t.Fatalf("Failed to read source file %s: %v", filePath, err)
							}

							// Normalize line endings
							content = bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n"))
							refactoredCode = strings.ReplaceAll(refactoredCode, "\r\n", "\n")

							// Check for the presence or absence of the refactored code on disk
							containsRefactored := bytes.Contains(content, []byte(refactoredCode))
							if opts.KeepRefactoredFiles {
								if !containsRefactored {
									t.Errorf("Expected file %s to contain the refactored code", filePath)
								}
							} else {
								if containsRefactored {
									t.Errorf("Expected file %s to NOT contain the refactored code", filePath)
								}
							}

							// Also make sure the refactored code isn't found in the original TestCase function declaration
							if funcDecl != "" && strings.Contains(funcDecl, refactoredCode) {
								t.Errorf("Refactored code should not be present in the original TestCase function declaration")
							}
						}
					}
				}
			}

			// Check that the results match the golden file
			g.AssertJson(t, tc.goldenName, data)
		})
	}
}
