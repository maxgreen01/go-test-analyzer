package parsercommands

import (
	"cmp"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dave/dst"
	"github.com/dave/dst/decorator"
	"github.com/gocarina/gocsv"
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
	AnalyzeOptions

	// Output file writer
	output *filewriter.FileWriter

	// Data fields
	testCases []*testcase.AnalysisResult // list of analysis results and related metadata for detected test functions

	tableDrivenTests            int // number of tests that are detected as table-driven
	refactorAttempts            int // total number of test cases that were attempted to be refactored
	refactorGenerationSuccesses int // number of test cases that were successfully refactored in some way
	refactorSuccesses           int // number of test cases whose execution results matched before and after refactoring
	tableDrivenLoops            int // if `--analyze-loops` is set, number of tests with loops that look table-driven
	tableDrivenConditionals     int // if `--analyze-conditionals` is set, number of table-driven tests with conditionals
}

// Command-line flags for the Analyze command specifically
type AnalyzeOptions struct {
	// todo LATER make RefactorStrategy a slice so multiple refactoring methods can be applied at once
	RefactorStrategy    string `long:"refactor" description:"The type of refactoring to perform on the detected test cases" choice:"none" choice:"subtest" default:"none"`
	KeepRefactoredFiles bool   `long:"keep-refactored-files" description:"Whether to retain the results of refactored test cases by NOT restoring the original source files after refactoring"`
	AnalyzeLoops        bool   `long:"analyze-loops" description:"Whether to perform an additional, more detailed analysis of all the loops detected in each test case"`
	AnalyzeConditionals bool   `long:"analyze-conditionals" description:"Whether to perform an additional, more detailed analysis of all the if/else chains detected in table-driven tests"`
	AnalyzeControlFlow  bool   `long:"analyze-control-flow" description:"Whether to perform a unified control flow analysis combining the loop and conditional analyses in table-driven tests"`
	CalculateComplexity bool   `long:"calculate-complexity" description:"Whether to calculate complexity metrics for detected table-driven tests and rank them by severity"`
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
// Note that `output` is copied as a pointer so `FileWriter` instances can be shared, but it is usually nil until `Execute()`.
func (cmd *AnalyzeCommand) Clone() parser.Task {
	globals := *cmd.globals
	return &AnalyzeCommand{
		globals:        &globals,
		AnalyzeOptions: cmd.AnalyzeOptions,
		output:         cmd.output,
	}
}

// Return the global configuration options
func (cmd *AnalyzeCommand) Config() *config.GlobalOptions {
	return cmd.globals
}

// Set the default output path if one is not provided, and initialize the output FileWriter instance
func (cmd *AnalyzeCommand) setupOutputWriter() error {
	if cmd.globals.OutputPath == "" {
		defaultFileName := fmt.Sprintf("%s-analyze-report.csv", filepath.Base(cmd.globals.ProjectDir))
		cmd.globals.OutputPath = filepath.Join(cmd.globals.OutputDir, defaultFileName)
	}
	// Initialize the output writer with the specified output path
	writer, err := filewriter.NewFileWriter(cmd.globals.OutputPath, cmd.globals.AppendOutput)
	if err != nil {
		return err
	}
	cmd.output = writer
	return nil
}

// Set the project directory for this task,
// and initialize a new output FileWriter if the OutputPath needs to be set based on the new dir.
func (cmd *AnalyzeCommand) SetProjectDir(dir string) error {
	cmd.globals.ProjectDir = dir

	// If splitting by dir and no output path was provided, each Task uses a different output file.
	// In this case, initialize the output writer now based on the new project dir.
	if cmd.globals.SplitByDir && cmd.globals.OutputPath == "" {
		return cmd.setupOutputWriter()
	}
	return nil
}

// Validate the values of this Command's flags, then run the task itself.
// THIS SHOULD ONLY BE CALLED ONCE PER PROGRAM EXECUTION.
func (cmd *AnalyzeCommand) Execute(args []string) error {
	// If there's only one output file needed (either not splitting, or splitting with a provided output path), set up the output writer now.
	// Otherwise (if splitting with multiple output files), wait until `SetProjectDir()` to do this.
	if !cmd.globals.SplitByDir || cmd.globals.OutputPath != "" {
		if err := cmd.setupOutputWriter(); err != nil {
			return err
		}
	}

	// The `--calculate-complexity` option requires control flow analysis to be enabled.
	if cmd.CalculateComplexity {
		cmd.AnalyzeControlFlow = true
	}

	// Process refactoring strategy. Allowed options are handled by the `choice` tag in the struct definition.
	cmd.RefactorStrategy = strings.ToLower(strings.TrimSpace(cmd.RefactorStrategy))

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
		analysisResult := testcase.Analyze(&tc, cmd.AnalyzeLoops, cmd.AnalyzeConditionals, cmd.AnalyzeControlFlow)
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

		// Only count loop analysis statistics if the `--analyze-loops` option is set
		if cmd.AnalyzeLoops && analysisResult.LoopAnalysis.CountTableDriven() > 0 {
			cmd.tableDrivenLoops++
		}

		// Only count conditional analysis statistics if the `--analyze-conditionals` option is set
		if cmd.AnalyzeConditionals && analysisResult.IfElseAnalysis.NumConditionals > 0 {
			cmd.tableDrivenConditionals++
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

		if cmd.AnalyzeLoops {
			reportLines = append(reportLines,
				"\n",
				fmt.Sprintf("Tests with table-driven loops: %d\n", cmd.tableDrivenLoops),
			)
		}

		if cmd.AnalyzeConditionals {
			reportLines = append(reportLines,
				"\n",
				fmt.Sprintf("Table-driven tests with conditionals: %d\n", cmd.tableDrivenConditionals),
			)
		}
	}

	// Save the optional complexity results to a static CSV file
	if cmd.CalculateComplexity {
		outFile, err := cmd.calculateComplexityScores()
		if err != nil {
			return fmt.Errorf("calculating complexity metrics: %w", err)
		}
		slog.Info("Complexity metrics calculated and saved to \"" + outFile + "\"")
	}

	// Print the main report to the terminal
	slog.Info("Finished running analysis task on project \"" + cmd.globals.ProjectDir + "\"")
	slog.Info("Writing results to \"" + cmd.output.GetPath() + "\"")
	fmt.Print(strings.Join(reportLines, "") + "\n")

	// Append the main results to output file (text or CSV)
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

// Close the main output file writer
func (cmd *AnalyzeCommand) Close() {
	if cmd.output != nil {
		cmd.output.Close()
	}
}

// Calculates complexity metrics for all detected table-driven test cases, ranks them by severity, and writes the results to a CSV file.
// Returns the path to the output file used, or an error if the operation could not be completed.
func (cmd *AnalyzeCommand) calculateComplexityScores() (string, error) {
	type scoredTest struct {
		Project                    string    `csv:"project"`
		FilePath                   string    `csv:"filePath"`
		ImportPath                 string    `csv:"importPath"`
		Name                       string    `csv:"name"`
		OverallScore               float32   `csv:"overallScore"`
		testcase.ComplexityMetrics `csv:"."` // inline the struct fields without a prefix
	}

	// Calculate complexity scores
	tests := make([]scoredTest, 0, len(cmd.testCases))
	for _, ar := range cmd.testCases {
		// Skip tests that are not table-driven
		if len(ar.ControlFlowStatements) == 0 || ar.TestCase == nil {
			continue
		}

		metrics := ar.CalculateComplexity()
		score := metrics.OverallScore()

		tests = append(tests, scoredTest{
			Project:           ar.TestCase.ProjectName,
			FilePath:          ar.TestCase.FilePath,
			ImportPath:        ar.TestCase.ImportPath,
			Name:              ar.TestCase.TestName,
			ComplexityMetrics: metrics,
			OverallScore:      score,
		})
	}

	// Sort tests by descending overall score, ascending import path (to group by packages), and ascending test name
	slices.SortFunc(tests, func(a, b scoredTest) int {
		return cmp.Or(
			cmp.Compare(b.OverallScore, a.OverallScore),
			cmp.Compare(a.ImportPath, b.ImportPath),
			cmp.Compare(a.Name, b.Name),
		)
	})

	// Write the results to a CSV file in the output directory
	scoresPath := filepath.Join(cmd.output.GetPathDir(), fmt.Sprintf("%s-complexity-scores.csv", filepath.Base(cmd.globals.ProjectDir)))
	if err := os.MkdirAll(filepath.Dir(scoresPath), 0755); err != nil {
		return scoresPath, err
	}

	file, err := os.OpenFile(scoresPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return scoresPath, err
	}
	defer file.Close()

	if err := gocsv.Marshal(&tests, file); err != nil {
		return scoresPath, err
	}

	return scoresPath, nil
}
