// Contains helper functions for running the analyzer against a sample project and reading its outputs.

package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gocarina/gocsv"

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

// sampleProjectPath is the absolute path to the temporary copy of the sample project directory (as set in `TestMain`), which is used during testing to enable package analysis.
var sampleProjectPath string

// TestMain copies the sample project contents to a temporary directory so the analyzer can run on it, since `go list` always ignores directories called "testdata".
func TestMain(m *testing.M) {
	retcode := 0
	// Run everything inside a function literal so the `defer` runs after `m.Run()` but before `os.Exit()`
	func() {
		tempDir, err := os.MkdirTemp("", "testdata-sampleproj-*")
		if err != nil {
			panic(err)
		}
		tempDir, err = filepath.Abs(tempDir)
		if err != nil {
			panic(err)
		}
		defer os.RemoveAll(tempDir)

		// Symlink the original sample project folder into the temp folder
		realProjectPath, err := filepath.Abs(originalProjectPath)
		if err != nil {
			panic(err)
		}
		linkedPath := filepath.Join(tempDir, samplePackage)

		if runtime.GOOS == "windows" {
			// Use a junction instead of a symlink, since symlinks require admin privileges
			cmd := exec.Command("cmd", "/c", "mklink", "/j", linkedPath, realProjectPath)
			if err := cmd.Run(); err != nil {
				panic(err)
			}
		} else {
			// On other platforms, just use a symlink
			if err := os.Symlink(realProjectPath, linkedPath); err != nil {
				panic(err)
			}
		}

		// Save the location-dependent test info
		sampleProjectPath = linkedPath

		// Actually run the tests
		retcode = m.Run()
	}()

	os.Exit(retcode)
}

// runAnalyzer runs the analyze command on the sample project with the given analyze-specific options and returns the command's parsed CSV report.
// This should be called once at the start of a top-level test function, and the returned value can be used repeatedly to check individual analysis results.
func runAnalyzer(t *testing.T, opts parsercommands.AnalyzeOptions) *analyzerResults {
	t.Helper()

	// Create a temp dir for the analyzer's output to keep each run isolated
	runDir := t.TempDir()
	reportPath := filepath.Join(runDir, samplePackage+"-analyze-report.csv")

	// Bypass CLI logic by setting up options directly
	cmdOpts := &config.GlobalOptions{
		ProjectDir:   sampleProjectPath,
		OutputPath:   reportPath,
		AppendOutput: false,
	}
	// FIXME do we need applyGlobals()? can't access things like BuildTags if not

	// Run the Analyze command directly
	cmd := parsercommands.NewAnalyzeCommand(cmdOpts)
	cmd.AnalyzeOptions = opts
	if err := cmd.Execute(nil); err != nil {
		t.Fatalf("Failed to run analyzer with options %+v: %v", opts, err)
	}

	// Parse the CSV report
	rows, err := readReport(reportPath)
	if err != nil {
		t.Fatalf("Failed to read CSV report: %v", err)
	}

	return &analyzerResults{rows: rows, outputDir: runDir}
}

// csvRow represents a single row in the analyzer's CSV report.
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
	LocalLoops       string `csv:"localLoops"`
	DelegatedLoops   string `csv:"delegatedLoops"`
	TableDrivenLoops string `csv:"tableDrivenLoops"`
}

// readReport reads the analyzer's CSV report and converts the data into structs.
func readReport(path string) ([]csvRow, error) {
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
	rows      []csvRow // CSV report rows in parsed order
	outputDir string   // output directory for this execution, e.g. containing per-package JSON files
}

// jsonFile reads the contents of the generated JSON file for a given test case.
func (r *analyzerResults) jsonFile(t *testing.T, testName string) []byte {
	t.Helper()

	testcase := testcase.TestCase{TestName: testName, PackageName: samplePackage, ImportPath: sampleImportPath, ProjectName: samplePackage}
	path := testcase.GetJSONFilePath(r.outputDir)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read JSON file for test %q at %q: %v", testName, path, err)
	}
	return data
}
