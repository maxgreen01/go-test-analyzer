package config

// Definitions for global command-line flags used across the entire application
type GlobalOptions struct {
	ProjectDir   string `long:"project" short:"p" description:"Path to the Go project directory to be parsed"`
	OutputPath   string `long:"output" short:"o" description:"Path to save the command's output report as a file"`
	AppendOutput bool   `long:"append" description:"Whether to append to the output file instead of overwriting it if the file already exists"`
	SplitByDir   bool   `long:"splitByDir" description:"Whether to parse each top-level directory separately (ignoring top-level Go files)"`
	Threads      int    `long:"threads" description:"The number of concurrent threads to use for parsing (only when splitting by directory)" default:"4"`

	LogLevel      string `long:"logLevel" short:"l" description:"The minimum severity of log message that should be displayed" choice:"debug" choice:"info" choice:"warn" choice:"error" default:"info"`
	TimestampLogs bool   `long:"timestamp-logs" description:"Whether to include the current timestamp (YYYYMMDD-HHMMSS) in the log file name"`

	BuildFlags []string `long:"build-flags" description:"Command-line options to pass to the underlying build tool"`
	BuildEnv   []string `long:"build-env" description:"Environment variables to pass to the underlying build tool, in KEY=VALUE format"`
}
