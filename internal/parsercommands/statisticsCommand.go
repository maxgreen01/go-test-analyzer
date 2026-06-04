package parsercommands

import (
	"fmt"
	"go/ast"
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
type StatisticsCommand struct {
	// Input flags
	globals *config.GlobalOptions // Avoid embedding this because the flag parser would treat it as duplicating the global options
	statisticsOptions

	// Output file writer
	output *filewriter.FileWriter

	// Data fields
	testCases []testcase.TestCase // list of actual test functions and related metadata

	testFileCount  int // total number of files ending in "_test.go"
	totalFileCount int // total number of Go files
	totalTestLines int // total number of lines in all test functions
	totalLines     int // total number of lines across the entire project
}

// Command-line flags for the Statistics command specifically
type statisticsOptions struct {
}

// Compile-time interface implementation check
var _ ParserCommand = (*StatisticsCommand)(nil)

// Register the command with the global flag parser
func init() {
	RegisterCommand(func(flagParser *flags.Parser, opts *config.GlobalOptions) {
		flagParser.AddCommand("statistics", "Collect statistics about a Go project's tests", "", NewStatisticsCommand(opts))
	})
}

// Create a new instance of the StatisticsCommand using a reference to the global options.
func NewStatisticsCommand(globals *config.GlobalOptions) *StatisticsCommand {
	return &StatisticsCommand{globals: globals}
}

func (cmd *StatisticsCommand) Name() string {
	return "statistics"
}

// Create a new instance of the StatisticsCommand with the same initial state and flags, COPYING `globals`.
// Note that `output` is shared by reference so `FileWriter` instances can be shared, but it is usually nil until `Execute()`.
func (cmd *StatisticsCommand) Clone() parser.Task {
	globals := *cmd.globals
	return &StatisticsCommand{
		globals:           &globals,
		statisticsOptions: cmd.statisticsOptions,
		output:            cmd.output,
	}
}

// Return the global configuration options
func (cmd *StatisticsCommand) Config() *config.GlobalOptions {
	return cmd.globals
}

// Set the project directory for this task.
func (cmd *StatisticsCommand) SetProjectDir(dir string) {
	cmd.globals.ProjectDir = dir
}

// Validate the values of this Command's flags, then run the task itself.
// THIS SHOULD ONLY BE CALLED ONCE PER PROGRAM EXECUTION.
func (cmd *StatisticsCommand) Execute(args []string) error {
	if cmd.globals.OutputPath == "" {
		cmd.globals.OutputPath = fmt.Sprintf("%s-statistics-report.csv", filepath.Base(cmd.globals.ProjectDir))
	}
	// Initialize the output writer with the specified output path
	writer, err := filewriter.NewFileWriter(cmd.globals.OutputPath, cmd.globals.AppendOutput)
	if err != nil {
		return fmt.Errorf("creating output writer for path %q", cmd.globals.OutputPath)
	}
	cmd.output = writer

	// Actually run the task by starting the parser
	return parser.Parse(cmd)
}

// Increment some numerical statistics about the project as a whole and its detected test cases
func (cmd *StatisticsCommand) Visit(file *dst.File, pkg *decorator.Package) {
	fset := pkg.Decorator.Fset
	astFile := pkg.Decorator.Ast.Nodes[file].(*ast.File)

	projectName := filepath.Base(cmd.globals.ProjectDir)
	packageName := file.Name.Name
	fileName := fset.Position(astFile.FileStart).Filename

	// increment project-scale statistics
	cmd.totalFileCount++
	if strings.HasSuffix(fileName, "_test.go") {
		cmd.testFileCount++
	}
	cmd.totalLines += fset.Position(astFile.End()).Line - fset.Position(astFile.FileStart).Line + 1

	// Only iterate top level declarations
	for _, decl := range file.Decls {
		fn, ok := decl.(*dst.FuncDecl)
		if !ok {
			continue
		}

		slog.Debug("Checking function...", "name", fn.Name.Name, "package", packageName, "file", fileName)

		// Save the function as a valid test case if it meets all the criteria
		valid, _ := testcase.IsValidTestCase(fn)
		if !valid {
			continue
		}

		tc := testcase.CreateTestCase(fn, file, pkg, projectName)
		cmd.testCases = append(cmd.testCases, tc)

		lines := tc.NumLines()
		cmd.totalTestLines += lines
	}
}

// Calculate some additional results and write everything to the output file
func (cmd *StatisticsCommand) ReportResults() error {
	// Format output for printing the report to the terminal (and potentially writing to a text file)

	reportLines := []string{
		fmt.Sprintf("\n=============  Statistics Report for %q:  =============\n\n", cmd.globals.ProjectDir),
	}

	// Define additional result statistics
	numTests := len(cmd.testCases)
	avgTestLines := 0.0
	percentTestLines := 0.0

	if numTests == 0 {
		reportLines = append(reportLines, "No test cases found in the specified project.\n\n")
	} else {
		// Calculate additional result statistics
		avgTestLines = float64(cmd.totalTestLines) / float64(numTests)
		percentTestLines = float64(cmd.totalTestLines) / float64(cmd.totalLines) * 100

		reportLines = append(reportLines,
			fmt.Sprintf("Total number of test cases: %d\n", numTests),
			"\n",
			fmt.Sprintf("Number of '_test.go' files: %d\n", cmd.testFileCount),
			fmt.Sprintf("Total number of Go files: %d\n", cmd.totalFileCount),
			"\n",
			fmt.Sprintf("Total lines of test code: %d\n", cmd.totalTestLines),
			fmt.Sprintf("Average lines per test case: %.1f\n", avgTestLines),
			fmt.Sprintf("Percentage of total lines for test cases: %.1f%%\n", percentTestLines),
			"\n",
		)
	}

	// Print the report to the terminal
	slog.Info("Finished running statistics task on project \"" + cmd.globals.ProjectDir + "\"")
	fmt.Print(strings.Join(reportLines, "") + "\n")

	// Append results to output file (text or CSV)
	switch cmd.output.DetectFormat() {

	case filewriter.FormatTxt:
		return cmd.output.Write(reportLines)

	case filewriter.FormatCSV:
		csvHeaders := []string{
			"projectDir",
			"testCases",
			"testFiles",
			"totalGoFiles",
			"testLines",
			"avgLinesPerTest",
			"percentTestLines",
		}

		row := []string{
			cmd.globals.ProjectDir,
			fmt.Sprintf("%d", numTests),
			fmt.Sprintf("%d", cmd.testFileCount),
			fmt.Sprintf("%d", cmd.totalFileCount),
			fmt.Sprintf("%d", cmd.totalTestLines),
			fmt.Sprintf("%.1f", avgTestLines),
			fmt.Sprintf("%.1f", percentTestLines),
		}

		return cmd.output.Write(row, csvHeaders)

	default:
		return fmt.Errorf("unsupported output format (file %q)", cmd.output.GetPath())
	}
}

// Close the output file writer
func (cmd *StatisticsCommand) Close() {
	if cmd.output != nil {
		cmd.output.Close()
	}
}
