package parsercommands

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/dave/dst"
	"github.com/dave/dst/decorator"
	"github.com/maxgreen01/go-test-analyzer/internal/config"
	"github.com/maxgreen01/go-test-analyzer/internal/filewriter"
	"github.com/maxgreen01/go-test-analyzer/pkg/parser"
	"github.com/maxgreen01/go-test-analyzer/pkg/testcase"

	"github.com/jessevdk/go-flags"
)

// Implementation of both the Parser Task interface and the Flags package's Commander interface.
// Stores input flags for the task, as well as fields representing the data to be collected.
type AnalyzeCommand struct {
	// Input flags
	globals *config.GlobalOptions // Avoid embedding this because the flag parser would treat it as duplicating the global options
	analyzeOptions

	// Output file writer
	output *filewriter.FileWriter

	// Data fields
	testCases []*testcase.AnalysisResult // list of analysis results and related metadata for detected test functions

	tableDrivenTests            int // number of tests that are table-driven
	refactorAttempts            int // total number of test cases that were attempted to be refactored
	refactorGenerationSuccesses int // number of test cases that were successfully refactored in some way
	refactorSuccesses           int // number of test cases whose execution results matched before and after refactoring
}

// Command-line flags for the Analyze command specifically
type analyzeOptions struct {
	// todo LATER/MAYBE make this a slice so multiple refactoring methods can be applied at once
	RefactorStrategy    string `long:"refactor" description:"The type of refactoring to perform on the detected test cases" choice:"none" choice:"subtest" default:"none"`
	KeepRefactoredFiles bool   `long:"keep-refactored-files" description:"Whether to retain the results of refactored test cases by NOT restoring the original source files after refactoring"`
}

// Compile-time interface implementation check
var _ ParserCommand = (*AnalyzeCommand)(nil)

// Register the command with the global flag parser
func init() {
	RegisterCommand(func(flagParser *flags.Parser, opts *config.GlobalOptions) {
		flagParser.AddCommand("analyze", "Analyze a Go project's table-driven tests", "", NewAnalyzeCommand(opts))
	})
}

// Create a new instance of the AnalyzeCommand using a reference to the global options.
func NewAnalyzeCommand(globals *config.GlobalOptions) *AnalyzeCommand {
	return &AnalyzeCommand{globals: globals}
}

func (cmd *AnalyzeCommand) Name() string {
	return "analyze"
}

// Create a new instance of the AnalyzeCommand with the same initial state and flags, COPYING `globals`.
// Note that `output` is shared by reference so `FileWriter` instances can be shared, but it is usually nil until `Execute()`.
func (cmd *AnalyzeCommand) Clone() parser.Task {
	globals := *cmd.globals
	return &AnalyzeCommand{
		globals:        &globals,
		analyzeOptions: cmd.analyzeOptions,
		output:         cmd.output,
	}
}

// Return the global configuration options
func (cmd *AnalyzeCommand) Config() *config.GlobalOptions {
	return cmd.globals
}

// Set the project directory for this task.
func (cmd *AnalyzeCommand) SetProjectDir(dir string) {
	cmd.globals.ProjectDir = dir
}

// Validate the values of this Command's flags, then run the task itself
// THIS SHOULD ONLY BE CALLED ONCE PER PROGRAM EXECUTION.
func (cmd *AnalyzeCommand) Execute(args []string) error {
	if cmd.globals.OutputPath == "" {
		cmd.globals.OutputPath = fmt.Sprintf("%s-analyze-report.csv", filepath.Base(cmd.globals.ProjectDir))
	}
	// Initialize the output writer with the specified output path
	writer, err := filewriter.NewFileWriter(cmd.globals.OutputPath, cmd.globals.AppendOutput)
	if err != nil {
		return err
	}
	cmd.output = writer

	// Validate refactoring strategy. Allowed options are handled by the `choice` tag in the struct definition.
	cmd.RefactorStrategy = strings.ToLower(strings.TrimSpace(cmd.RefactorStrategy))

	// Actually run the task by starting the parser
	return parser.Parse(cmd)
}

// Extract test cases from the given file, analyze them, and potentially refactor them before saving the results to JSON files.
func (cmd *AnalyzeCommand) Visit(file *dst.File, pkg *decorator.Package) {
	projectName := filepath.Base(cmd.globals.ProjectDir)
	// packageName := file.Name.Name
	// filePath := pkg.Decorator.Fset.Position(file.FileStart).Filename

	// Only iterate top level declarations
	for _, decl := range file.Decls {
		fn, ok := decl.(*dst.FuncDecl)
		if !ok {
			continue
		}

		// slog.Debug("Checking function...", "name", fn.Name.Name, "package", packageName, "file", filePath)

		// Save the function as a valid test case if it meets all the criteria
		valid, _ := testcase.IsValidTestCase(fn)
		// todo do something with the `badFormat` return value
		if !valid {
			continue
		}
		tc := testcase.CreateTestCase(fn, file, pkg, projectName)

		// Analyze and store the test case
		analysisResult := testcase.Analyze(&tc)
		cmd.testCases = append(cmd.testCases, analysisResult)

		if analysisResult.IsTableDriven() {
			cmd.tableDrivenTests++
		}

		// Attempt to refactor the test case if a refactoring strategy is specified
		result := analysisResult.AttemptRefactoring(testcase.RefactorStrategyFromString(cmd.RefactorStrategy), cmd.KeepRefactoredFiles)

		// Only count refactoring statistics if a refactoring strategy was specified
		if result.Strategy != testcase.RefactorStrategyNone && result.GenerationStatus != testcase.RefactorGenerationStatusNone {
			// A refactoring attempt was made
			cmd.refactorAttempts++

			if result.GenerationStatus == testcase.RefactorGenerationStatusSuccess {
				// The refactoring generation succeeded
				cmd.refactorGenerationSuccesses++

				if result.OriginalExecutionResult == result.RefactoredExecutionResult && result.OriginalExecutionResult == testcase.TestExecutionResultPass {
					// The refactoring generation was successful, and the execution results are both successful too
					cmd.refactorSuccesses++
				}
			}
		}

		// Write all results to a JSON file
		err := analysisResult.SaveAsJSON(cmd.output.GetPathDir())
		if err != nil {
			slog.Error("Saving test case as JSON", "err", err, "test", tc)
		}
	}
}

// Summarize the results of the entire analysis in one file, leaving the bulk of the specific data about each
// test case in its corresponding JSON file that was saved previously.
func (cmd *AnalyzeCommand) ReportResults() error {
	// Format output for printing the report to the terminal (and potentially writing to a text file)

	reportLines := []string{
		fmt.Sprintf("\n=============  Analysis Report for %q:  =============\n\n", cmd.globals.ProjectDir),
	}

	numTests := len(cmd.testCases)

	if numTests == 0 {
		reportLines = append(reportLines, "No test cases found in the specified project.\n\n")
	} else {
		reportLines = append(reportLines,
			fmt.Sprintf("Number of test cases: %d\n", numTests),
			"\n",
			fmt.Sprintf("Table-driven tests: %d\n", cmd.tableDrivenTests),
			"\n",
			fmt.Sprintf("Refactoring strategy: %q\n", cmd.RefactorStrategy),
		)

		if cmd.RefactorStrategy != "none" { // todo CLEANUP don't hardcode this
			reportLines = append(reportLines,
				fmt.Sprintf("Refactoring attempts: %d\n", cmd.refactorAttempts),
				fmt.Sprintf("Refactor generation successes: %d\n", cmd.refactorGenerationSuccesses),
				fmt.Sprintf("Refactoring successes (with successful execution): %d\n", cmd.refactorSuccesses),
			)
		}
	}

	// Print the report to the terminal
	slog.Info("Finished running analysis task on project \"" + cmd.globals.ProjectDir + "\"")
	fmt.Print(strings.Join(reportLines, "") + "\n")

	// Append results to output file (text or CSV)
	switch cmd.output.DetectFormat() {

	case filewriter.FormatTxt:
		return cmd.output.Write(reportLines)

	case filewriter.FormatCSV:
		if numTests == 0 {
			return nil
		}

		// Save a condensed version of each analyzed test case
		rows := make([][]string, 0, numTests)
		for _, tc := range cmd.testCases {
			rows = append(rows, tc.EncodeAsCSV())
		}
		return cmd.output.WriteMultiple(rows, cmd.testCases[0].GetCSVHeaders())

	default:
		return fmt.Errorf("unsupported output format (file %q)", cmd.output.GetPath())
	}
}

// Close the output file writer
func (cmd *AnalyzeCommand) Close() {
	if cmd.output != nil {
		cmd.output.Close()
	}
}
