// Main application entry point
package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/maxgreen01/go-test-analyzer/internal/config"
	"github.com/maxgreen01/go-test-analyzer/internal/filewriter"
	"github.com/maxgreen01/go-test-analyzer/internal/parsercommands"
	"github.com/maxgreen01/go-test-analyzer/pkg/parser"

	"github.com/jessevdk/go-flags"
	"github.com/lmittmann/tint"
	"github.com/mattn/go-colorable"
	slogmulti "github.com/samber/slog-multi"
)

var version = "undefined" // replaced at build time using Makefile linker flags

// =========== Parse command-line flags and initialize the application ===========
func main() {
	// Create the flag parser itself
	var opts config.GlobalOptions
	flagParser := flags.NewParser(&opts, flags.Default|flags.AllowBoolValues)

	// Dynamically add commands from the registry
	for _, registerFunc := range parsercommands.CommandRegistry {
		registerFunc(flagParser, &opts)
	}

	// Manually add the version command
	flagParser.AddCommand("version", "Show this application's version number", "", &VersionCommand{})

	// Set up a hook to validate and apply global flags before executing any command.
	// Also handles logic for after the command finishes executing using `defer`.
	flagParser.CommandHandler = func(command flags.Commander, args []string) error {
		if command == nil {
			return nil
		} else if _, ok := command.(*VersionCommand); ok {
			// Don't parse flags for version command
			return command.Execute(args)
		}

		task, ok := command.(parser.Task)
		if !ok {
			slog.Error("Command does not implement the Task interface")
			os.Exit(1)
		}

		// Validate and apply global flags
		applyGlobals(&opts)

		// Print the current configuration options for the command being executed by iterating through
		// the active `flags.Command` struct (different than the `flags.Commander` interface) and
		// saving the command-line option values at each step.
		optionValues := []any{"version", version, "command", task.Name()}
		activeCmd := flagParser.Command
		for activeCmd != nil {
			for _, group := range append(activeCmd.Groups(), activeCmd.Group) {
				if group.ShortDescription == "" || group.ShortDescription == "Help Options" {
					continue // Skip "fake" groups
				}
				for _, option := range group.Options() {
					optionValues = append(optionValues, option.LongNameWithNamespace(), option.Value())
				}
			}
			activeCmd = activeCmd.Active // Move to the next nested command, if any
		}

		slog.Info("Starting go-test-analyzer", optionValues...)

		// Set up timer hook
		startTime := time.Now()
		defer func() {
			// Runs after the command finishes executing
			fmt.Printf("Total execution time: %v\n\n", time.Since(startTime))
		}()

		// Actually execute the command (which starts the parser)
		if err := command.Execute(args); err != nil {
			slog.Error("Error parsing project", "err", err, "task", task.Name(), "project", opts.ProjectDir)
			os.Exit(1)
		}

		return nil
	}

	// Actually run the flag parser and start the application, or display the help menu
	_, err := flagParser.Parse()
	if err != nil {
		// Exit successfully when printing the help menu, but with a failure code otherwise
		if flags.WroteHelp(err) {
			os.Exit(0)
		}
		os.Exit(1)
	}
}

// Validate (in-place) and apply global flags such as logging level and color output
func applyGlobals(opts *config.GlobalOptions) {
	//
	// =========== Validate flag values ===========
	//

	// Validate the project directory, resolve it to an absolute path, and check that it exists and is a directory
	opts.ProjectDir = strings.Trim(opts.ProjectDir, "\t\n\v\f\r \"") // Trim whitespace and quotes
	if opts.ProjectDir == "" {
		fmt.Fprintf(os.Stderr, "You must provide a path to a Go project (e.g., ./myproject)!\n")
		os.Exit(1)
	}
	absProjectPath, err := filepath.Abs(opts.ProjectDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving absolute path to Go project %q: %v\n", opts.ProjectDir, err)
		os.Exit(1)
	}
	info, err := os.Stat(absProjectPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error accessing project path %q: %v\n", absProjectPath, err)
		os.Exit(1)
	}
	if !info.IsDir() {
		fmt.Fprintf(os.Stderr, "Provided project path %q is not a directory!\n", absProjectPath)
		os.Exit(1)
	}
	opts.ProjectDir = absProjectPath

	// Validate and resolve the output path, if provided. This ensures that any provided path that includes a directory is resolved relative
	// to the current working directory instead of the default output path. Additional validation and processing is done by FileWriter.
	// The default value may be set within each command to ensure proper functionality with `split-by-dir`.
	opts.OutputPath = strings.Trim(opts.OutputPath, "\t\n\v\f\r \"") // Trim whitespace and quotes
	if opts.OutputPath != "" && strings.HasPrefix(opts.OutputPath, ".") {
		absOutputPath, err := filepath.Abs(opts.OutputPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error resolving absolute path for output file %q: %v\n", opts.OutputPath, err)
			os.Exit(1)
		}
		opts.OutputPath = absOutputPath
	}

	// In the specific case where `split-by-dir` is enabled, a specific output file is provided, and `append` is disabled, we need to truncate the file originally
	// (as the user expects), but implicitly enable appending during the command execution so the results don't overwrite each other.
	if opts.SplitByDir && opts.OutputPath != "" && !opts.AppendOutput {
		// Set up a temporary FileWriter instance to truncate the file
		writer, err := filewriter.NewFileWriter(opts.OutputPath, false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error truncating output file %q: %v\n", opts.OutputPath, err)
			os.Exit(1)
		}
		writer.Close()
		opts.AppendOutput = true
	}

	// Validate the number of threads used if splitting by directory
	if opts.Threads < 1 {
		fmt.Fprintf(os.Stderr, "Invalid number of threads %d, must be at least 1\n", opts.Threads)
		os.Exit(1)
	}

	// Process log level. Allowed options are handled by the `choice` tag in the struct definition.
	opts.LogLevel = strings.ToLower(strings.TrimSpace(opts.LogLevel))

	// Process and validate command-line options for the build tool
	var cleanFlags []string
	for _, f := range opts.BuildFlags {
		f = strings.TrimSpace(f)
		if !strings.HasPrefix(f, "-") {
			fmt.Fprintf(os.Stderr, "Invalid build flag %q, must start with a hyphen\n", f)
			os.Exit(1)
		}
		cleanFlags = append(cleanFlags, f)
	}
	opts.BuildFlags = cleanFlags

	// Process and validate environment variables for the build tool
	var cleanEnv []string
	for _, e := range opts.BuildEnv {
		e = strings.TrimSpace(e)
		if !strings.Contains(e, "=") {
			fmt.Fprintf(os.Stderr, "Invalid build environment variable %q, must be in KEY=VALUE format\n", e)
			os.Exit(1)
		}
		cleanEnv = append(cleanEnv, e)
	}
	opts.BuildEnv = cleanEnv

	// Map log level string value to a `slog.Level`
	var level slog.Level
	switch opts.LogLevel {
	case "info":
		level = slog.LevelInfo
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		// Should never happen because log level options should be validated already
		fmt.Fprintf(os.Stderr, "Invalid log level %q", opts.LogLevel)
		os.Exit(1)
	}

	//
	// =========== Set up the logger for program-wide use ===========
	//
	// Aim to distribute logs to both `stderr` and a log file.
	// Note that logs are written to `stderr` instead of `stdout` to separate them from the main output of the program.

	var handlers []slog.Handler

	// Crate `stderr` handler (with color output support)
	handlers = append(handlers,
		tint.NewHandler(colorable.NewColorableStderr(), &tint.Options{
			Level:      level,
			TimeFormat: time.DateTime,
			ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
				// Write `error` values in red
				if a.Value.Kind() == slog.KindAny {
					if _, ok := a.Value.Any().(error); ok {
						return tint.Attr(9, a)
					}
				}
				return a
			},
		}),
	)

	// Attempt to set up the log file at `logs/analyzer.log` (or `logs/analyzer-<timestamp>.log`) relative to the default output directory.
	// Don't crash if setup fails because the program will still print logs to `stderr`.
	outputDir, dirErr := filewriter.GetDefaultOutputDir()
	if dirErr != nil {
		fmt.Fprintf(os.Stderr, "Could not determine default output directory for logs: %v\n", dirErr)
	} else {
		logFileName := "analyzer.log"
		if opts.TimestampLogs {
			// Format the current time as YYYYMMDD-HHMMSS to avoid overwriting previous logs
			logFileName = fmt.Sprintf("analyzer-%s.log", time.Now().Format("20060102-150405"))
		}
		logFilePath := filepath.Join(outputDir, "logs", logFileName)
		if err := os.MkdirAll(filepath.Dir(logFilePath), 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Could not create log directory %q: %v\n", filepath.Dir(logFilePath), err)
		}
		logFile, fileErr := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)

		if fileErr != nil {
			fmt.Fprintf(os.Stderr, "Could not open log file %q: %v\n", logFilePath, fileErr)
		} else {
			// Create a handler to write logs to the file if it was successfully opened
			handlers = append(handlers, slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: level}))
		}
	}

	// Put the loggers together and set the default logger
	slog.SetDefault(slog.New(
		slogmulti.Fanout(handlers...),
	))
}

// Define the version command
type VersionCommand struct{}

func (c *VersionCommand) Execute(_ []string) error {
	fmt.Printf("go-test-analyzer %s\n", version)
	fmt.Printf("- os/type: %s\n", runtime.GOOS)
	fmt.Printf("- os/arch: %s\n", runtime.GOARCH)
	fmt.Printf("- go/version: %s\n", runtime.Version())
	return nil
}
