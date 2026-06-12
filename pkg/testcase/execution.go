package testcase

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

//
// ========== Test Execution ==========
//

// Represents the result of a test execution, as run by the `go test` command.
type TestExecutionResult int

const (
	TestExecutionResultNotRun           TestExecutionResult = iota // The test was not executed
	TestExecutionResultUnknown                                     // The test result could not be determined
	TestExecutionResultCompilationError                            // The test failed to compile
	TestExecutionResultSkip                                        // The test was skipped
	TestExecutionResultFail                                        // The test failed
	TestExecutionResultPass                                        // The test passed successfully
)

func (ter TestExecutionResult) String() string {
	switch ter {
	case TestExecutionResultUnknown:
		return "unknown"
	case TestExecutionResultNotRun:
		return "notRun"
	case TestExecutionResultCompilationError:
		return "compilationError"
	case TestExecutionResultSkip:
		return "skip"
	case TestExecutionResultFail:
		return "fail"
	case TestExecutionResultPass:
		return "pass"
	default:
		return "unknown"
	}
}

func (ter TestExecutionResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(ter.String())
}

func (ter *TestExecutionResult) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}
	switch strings.ToLower(str) {
	case "unknown":
		*ter = TestExecutionResultUnknown
	case "notRun":
		*ter = TestExecutionResultNotRun
	case "compilationError":
		*ter = TestExecutionResultCompilationError
	case "skip":
		*ter = TestExecutionResultSkip
	case "fail":
		*ter = TestExecutionResultFail
	case "pass":
		*ter = TestExecutionResultPass
	default:
		slog.Warn("Unknown test execution result", "result", str)
		*ter = TestExecutionResultNotRun
	}
	return nil
}

// Represents a single JSON line event from `go test -json`.
// See https://pkg.go.dev/cmd/test2json for details.
type executionEvent struct {
	Time        time.Time `json:"Time"` // encodes as an RFC3339-format string
	Action      string    `json:"Action"`
	Package     string    `json:"Package"`
	Test        string    `json:"Test"`
	Elapsed     float64   `json:"Elapsed"` // seconds
	Output      string    `json:"Output"`
	FailedBuild string    `json:"FailedBuild"`
}

// Execute a test based on the contents of its corresponding file in the file system using `go test`, and return the results.
// Returns an error if the test fails for any reason.
func (tc *TestCase) Execute() (TestExecutionResult, error) {
	if tc.FilePath == "" || tc.TestName == "" {
		return TestExecutionResultNotRun, fmt.Errorf("missing FilePath or TestName in TestCase: %v", tc)
	}
	importPath := tc.GetImportPath()
	if importPath == "" {
		return TestExecutionResultNotRun, fmt.Errorf("missing ImportPath in TestCase: %v", tc)
	}
	importPath = strings.TrimSuffix(importPath, "_test") // `go test` fails to compile because of a missing module unless we remove the `_test` suffix

	slog.Debug("Executing test case", "file", tc.FilePath, "test", tc)

	// Build the go test command with JSON output
	testPattern := fmt.Sprintf("^%s$", tc.TestName)
	cmd := []string{"go", "test", importPath, "-run", testPattern, "-v", "-json"}
	c := exec.Command(cmd[0], cmd[1:]...)
	c.Dir = filepath.Dir(tc.FilePath) // Use the directory of the test file as the working directory

	// Execute the command and save the output
	// TODO CLEANUP maybe make this concurrent somehow?
	jsonBytes, err := c.Output()

	// Parse the JSON output and determine the test result
	var evt executionEvent
	var prevEvts []executionEvent
	subtestResults := make(map[string]string) // Map between subtest name and corresponding result ("pass", "fail", "skip")
	dec := json.NewDecoder(bytes.NewReader(jsonBytes))
	for {
		// Decode test output into JSON events
		if err := dec.Decode(&evt); err == io.EOF {
			break
		} else if err != nil {
			return TestExecutionResultUnknown, fmt.Errorf("parsing test output as JSON: %w", err)
		}

		// Inspect the event to determine the test result

		// Check for compilation error
		if evt.Action == "build-fail" {
			// Find the first non-empty line of test output otherwise
			errorStr := "[no output]"
			for _, e := range prevEvts {
				// Heuristic: lines starting with "# " are likely to be headers for the package being compiled, which are less informative
				if (e.Action == "output" || e.Action == "build-output") && !strings.HasPrefix(e.Output, "# ") {
					errorStr = strings.TrimSpace(e.Output)
					break
				}
			}
			return TestExecutionResultCompilationError, fmt.Errorf("compilation error: %s", errorStr)
		}

		// Check for invalid test name (because the test should definitely exist)
		if evt.Action == "output" && strings.Contains(evt.Output, "no tests to run") || strings.Contains(evt.Output, "no test files") {
			return TestExecutionResultUnknown, fmt.Errorf("no tests to run for pattern %q in file %q", testPattern, tc.FilePath)
		}

		// A test (or subtest) finished running
		if evt.Action == "pass" || evt.Action == "fail" || evt.Action == "skip" {
			// Check the result of the overall test
			failed := false
			if evt.Test == tc.TestName {
				switch evt.Action {
				case "pass":
					if len(subtestResults) > 0 {
						slog.Debug("Test passed with subtest results", "test", tc.TestName, "subtestResults", formatSubtestResults(subtestResults))
					}
					return TestExecutionResultPass, nil

				case "skip":
					return TestExecutionResultSkip, nil

				case "fail":
					failed = true // Actual logic executed below
				}
			}

			// If the entire package failed before running the test itself, still log this as a failure
			if failed || (evt.Action == "fail" && evt.Test == "") {
				// Include the failing subtests if possible
				if len(subtestResults) > 0 {
					return TestExecutionResultFail, fmt.Errorf("test failed with subtests: %v", formatSubtestResults(subtestResults))
				}
				// Find the first non-empty line of test output otherwise
				errorStr := "[no output]"
				for _, e := range prevEvts {
					// Heuristic: lines starting with "---" or "===" are likely to be headers for the test being run, which are less informative
					if e.Action == "output" && !strings.HasPrefix(e.Output, "---") && !strings.HasPrefix(e.Output, "===") {
						errorStr = strings.TrimSpace(e.Output)
						break
					}
				}
				return TestExecutionResultFail, fmt.Errorf("test failed: %s", errorStr)
			}

			// Track subtest results
			if slashIdx := strings.Index(evt.Test, "/"); slashIdx != -1 {
				subtestResults[evt.Test[slashIdx+1:]] = evt.Action
			}
		}

		// Parse the next event
		prevEvts = append(prevEvts, evt)
	}

	// Fallback: unknown result
	if err != nil {
		return TestExecutionResultFail, fmt.Errorf("unknown test error: %w", err)
	}
	return TestExecutionResultUnknown, fmt.Errorf("unknown test result: %s", string(jsonBytes))
}

// Convert subtest results to a human-readable string for logging and error messages
func formatSubtestResults(subtestResults map[string]string) string {
	var sb strings.Builder
	for subtest, result := range subtestResults {
		fmt.Fprintf(&sb, "%s: %s\n", subtest, result)
	}
	return sb.String()
}
