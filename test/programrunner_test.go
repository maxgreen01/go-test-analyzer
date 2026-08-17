package test

// This file contains helper functions for running the analyzer against a sample project and reading its outputs.

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gocarina/gocsv"
	slogmulti "github.com/samber/slog-multi"

	"github.com/maxgreen01/go-test-analyzer/internal/config"
	"github.com/maxgreen01/go-test-analyzer/internal/parsercommands"
	"github.com/maxgreen01/go-test-analyzer/pkg/testcase"
)

// Always-known data about the sample project being analyzed
const (
	// samplePackage is the name of the sample project's package, which is also the name of the folder containing it.
	samplePackage = "sampleproj"

	// originalProjectPath is the relative path to the actual source directory for the sample project, which `go list` ignores because of the "testdata" directory name.
	originalProjectPath = "./testdata/" + samplePackage

	// sampleImportPath is the import path of the sample project package.
	sampleImportPath = "github.com/maxgreen01/go-test-analyzer/test/testdata/" + samplePackage
)

// runAnalyzer runs the analyze command on a fresh temporary copy of the sample project with the given options, and returns the parsed CSV report results.
func runAnalyzer(t *testing.T, opts parsercommands.AnalyzeOptions) *analyzerResults {
	t.Helper()

	// Create a temp dir to keep each run's source files and outputs isolated
	runDir := t.TempDir()
	projectDir := filepath.Join(runDir, samplePackage)
	outputDir := filepath.Join(runDir, "output")

	srcProjDir, err := filepath.Abs(originalProjectPath)
	if err != nil {
		t.Fatalf("Failed to get absolute path of sample project: %v", err)
	}

	if err := copyDir(srcProjDir, projectDir); err != nil {
		t.Fatalf("Failed to copy sample project to temp directory: %v", err)
	}

	reportPath := filepath.Join(outputDir, samplePackage+"-analyze-report.csv")

	// Bypass CLI logic by setting up options directly
	cmdOpts := &config.GlobalOptions{
		ProjectDir:   projectDir,
		OutputPath:   reportPath,
		AppendOutput: false,
	}
	// todo LATER do we need applyGlobals()? can't access things like BuildTags if not

	// Capture the log output so it can be checked in tests if needed, and also log to stderr (like usual) for compatibility with `go test -v`.
	// Note that reusing `oldDefault` inside the fanout causes the program to hang, so we must create a new text handler instead.
	var logBuf bytes.Buffer
	oldDefault := slog.Default()
	slog.SetDefault(slog.New(slogmulti.Fanout(
		slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{}),
		slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{}),
	)))
	t.Cleanup(func() {
		slog.SetDefault(oldDefault)
	})

	// Run the Analyze command directly
	cmd := parsercommands.NewAnalyzeCommand(cmdOpts)
	cmd.AnalyzeOptions = opts
	if err := cmd.Execute(nil); err != nil {
		t.Fatalf("Failed to run analyzer on %s with options %+v: %v", projectDir, opts, err)
	}

	// Parse the CSV report
	rows, err := readReportCSV(reportPath)
	if err != nil {
		t.Fatalf("Failed to read CSV report: %v", err)
	}

	return &analyzerResults{rows: rows, projectDir: projectDir, outputDir: outputDir, logBuf: logBuf}
}

// readReportCSV reads the analyzer's CSV report and converts the data into structs.
func readReportCSV(path string) ([]csvRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var rows []csvRow
	if err := gocsv.UnmarshalFile(f, &rows); err != nil { // Load rows from file
		return nil, err
	}
	return rows, nil
}

// analyzerResults holds the parsed output of one execution of the Analyze command.
type analyzerResults struct {
	rows       []csvRow     // CSV report rows in parsed order
	projectDir string       // the copied project directory where the code was analyzed and refactored
	outputDir  string       // output directory for this execution, e.g. containing per-package JSON files
	logBuf     bytes.Buffer // captured log output from this execution
}

// jsonFile reads the contents of the generated JSON file for a given test case.
func (r *analyzerResults) jsonFile(t *testing.T, testName string) []byte {
	t.Helper()

	projName := filepath.Base(r.projectDir)
	testcase := testcase.TestCase{
		TestName:    testName,
		PackageName: samplePackage,
		ImportPath:  sampleImportPath,
		ProjectName: projName,
	}
	path := testcase.GetJSONFilePath(r.outputDir)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read JSON file for test %q at %q: %v", testName, path, err)
	}
	return data
}

// checkErrorLog searches for any ERROR-level log entries in the captured log output, and fails the test if any are found.
func (r *analyzerResults) checkErrorLog(t *testing.T) {
	for line := range strings.SplitSeq(strings.TrimSpace(r.logBuf.String()), "\n") {
		if strings.Contains(line, `"level":"ERROR"`) {
			t.Errorf("Unexpected error log detected: %s", line)
		}
	}
}

// csvRow represents a single row in the analyzer's CSV report.
// !! THIS STRUCT MUST BE UPDATED WHENEVER THE CSV REPORT FORMAT CHANGES !!
type csvRow struct {
	// Regular fields
	Project                   string `csv:"project"`
	FilePath                  string `csv:"filePath"`
	ImportPath                string `csv:"importPath"`
	Name                      string `csv:"name"`
	IsTableDriven             string `csv:"isTableDriven"`
	ScenarioDataStructure     string `csv:"scenarioDataStructure"`
	ScenarioCount             string `csv:"scenarioCount"`
	ScenarioNameField         string `csv:"scenarioNameField"`
	ScenarioExpectedFields    string `csv:"scenarioExpectedFields"`
	ScenarioHasFunctionFields string `csv:"scenarioHasFunctionFields"`
	ScenarioUsesSubtest       string `csv:"scenarioUsesSubtest"`

	// Refactoring fields
	RefactorStrategy          string `csv:"refactorStrategy"`
	RefactorGenerationStatus  string `csv:"refactorGenerationStatus"`
	OriginalExecutionResult   string `csv:"originalExecutionResult"`
	RefactoredExecutionResult string `csv:"refactoredExecutionResult"`

	ImportedPackages string `csv:"importedPackages"`

	// Loop analysis fields (may not be present)
	NumLoops         string `csv:"numLoops"`
	TableDrivenLoops string `csv:"tableDrivenLoops"`

	// Conditional analysis fields (may not be present)
	NumConditionals string `csv:"numConditionals"`

	// Control flow analysis fields (may not be present)
	NumControlFlowStatements string `csv:"numControlFlowStatements"`
}

// copyDir recursively copies a directory tree from src to dst. It does not preserve file permissions or timestamps, does not follow symlinks, and overwrites existing files in the destination.
func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			data, err := os.ReadFile(srcPath)
			if err != nil {
				return err
			}
			if err := os.WriteFile(dstPath, data, 0644); err != nil {
				return err
			}
		}
	}
	return nil
}
