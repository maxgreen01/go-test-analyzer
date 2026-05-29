package testcase

import (
	"encoding/json"
	"go/ast"
	"go/token"
	"log/slog"
	"strings"

	"github.com/maxgreen01/go-test-analyzer/pkg/asttools"
)

// Represents the result of a refactoring attempt on a test case.
type RefactorResult struct {
	// The refactoring strategy that was applied, if any
	Strategy RefactorStrategy `json:"strategy"`

	// The status of the refactor generation attempt
	GenerationStatus RefactorGenerationStatus `json:"status"`

	// The contents of the refactored test case, if the refactor generation was successful
	Refactorings []RefactoredFunction `json:"refactorings"`

	// The results of executing the test case before and after refactoring
	OriginalExecutionResult   TestExecutionResult `json:"originalTestResult"`
	RefactoredExecutionResult TestExecutionResult `json:"refactoredTestResult"`
}

//
// =============== Supporting Type Definitions ===============
//

// Represents a refactoring strategy that can be applied to a test case.
// Each value corresponds to a refactoring method with a similar name.
type RefactorStrategy int

const (
	RefactorStrategyNone    RefactorStrategy = iota // No refactoring method specified
	RefactorStrategySubtest                         // Wrap the entire contents of the execution loop in a call to `t.Run()`
)

// Return the RefactorStrategy corresponding to the given string.
func RefactorStrategyFromString(method string) RefactorStrategy {
	switch strings.ToLower(method) {
	case "subtest":
		return RefactorStrategySubtest
	case "none":
		return RefactorStrategyNone
	default:
		slog.Warn("Unknown refactoring strategy", "strategy", method)
		return RefactorStrategyNone
	}
}

func (rm RefactorStrategy) String() string {
	switch rm {
	case RefactorStrategySubtest:
		return "subtest"
	default:
		return "none"
	}
}

func (rm RefactorStrategy) MarshalJSON() ([]byte, error) {
	return json.Marshal(rm.String())
}

func (rm *RefactorStrategy) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}
	*rm = RefactorStrategyFromString(str)
	return nil
}

// Represents the status of an attempt to generate refactored code for a test case.
type RefactorGenerationStatus int

const (
	RefactorGenerationStatusNone      RefactorGenerationStatus = iota // No refactoring was attempted
	RefactorGenerationStatusError                                     // Refactoring could not be performed properly due to an unrecoverable error, e.g. due to a logic error
	RefactorGenerationStatusBadFields                                 // Refactoring failed based on the configuration of the scenario fields
	RefactorGenerationStatusNoTester                                  // Refactoring failed because a `*testing.T` variable could not be detected
	RefactorGenerationStatusFail                                      // Refactoring failed unexpectedly, e.g. due to an unusual AST structure
	RefactorGenerationStatusSuccess                                   // Refactoring was successful
)

func (rs RefactorGenerationStatus) String() string {
	switch rs {
	case RefactorGenerationStatusNone:
		return "none"
	case RefactorGenerationStatusError:
		return "error"
	case RefactorGenerationStatusBadFields:
		return "badFields"
	case RefactorGenerationStatusNoTester:
		return "noTester"
	case RefactorGenerationStatusFail:
		return "fail"
	case RefactorGenerationStatusSuccess:
		return "success"
	default:
		return "unknown"
	}
}

func (rs RefactorGenerationStatus) MarshalJSON() ([]byte, error) {
	return json.Marshal(rs.String())
}

func (rs *RefactorGenerationStatus) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}
	switch strings.ToLower(str) {
	case "none":
		*rs = RefactorGenerationStatusNone
	case "error":
		*rs = RefactorGenerationStatusError
	case "badFields":
		*rs = RefactorGenerationStatusBadFields
	case "noTester":
		*rs = RefactorGenerationStatusNoTester
	case "fail":
		*rs = RefactorGenerationStatusFail
	case "success":
		*rs = RefactorGenerationStatusSuccess
	default:
		slog.Warn("Unknown refactoring strategy", "strategy", str)
		*rs = RefactorGenerationStatusNone
	}
	return nil
}

// Represents function declaration that has been refactored.
// Note that the AST file is not guaranteed to retain the modified function data after Cleanup is called,
// but it should still be retained by the FuncDecl reference (and by the string forms of these AST elements).
type RefactoredFunction struct {
	Refactored       *ast.FuncDecl `json:"-"`          // The actual refactored function declaration
	RefactoredString string        `json:"refactored"` // The string representation of the refactored function declaration

	File     *ast.File `json:"-"`        // The AST file where the refactored function is defined
	FilePath string    `json:"filePath"` // The path to the file containing the refactored function

	cleanup func() error // A function to restore the original function declaration if necessary
	// todo CLEANUP maybe replace with storing the original AST function and file and performing the cleanup based on that
}

// Creates a new RefactoredFunction with the provided AST data.
func NewRefactoredFunction(fn *ast.FuncDecl, file *ast.File, cleanupFunc func() error, fset *token.FileSet) *RefactoredFunction {
	if fn == nil || file == nil {
		slog.Error("Cannot create RefactoredFunction with nil syntax data", "funcDecl", fn, "file", file)
		return nil
	}
	if fset == nil {
		slog.Error("Cannot create RefactoredFunction with nil FileSet", "funcDecl", fn, "file", file)
		return nil
	}

	return &RefactoredFunction{
		Refactored:       fn,
		RefactoredString: asttools.NodeToString(fn, fset),

		File:     file,
		FilePath: fset.Position(file.FileStart).Filename,

		cleanup: cleanupFunc,
	}
}

// Performs an in-place update of the string representation of the refactored AST function declaration
// already stored in the RefactoredFunction, using the provided FileSet.
func (rf *RefactoredFunction) UpdateStringRepresentation(fset *token.FileSet) {
	if rf.Refactored == nil || rf.File == nil || fset == nil {
		slog.Error("Cannot update RefactoredFunction strings with nil syntax data", "refactored", rf.Refactored, "file", rf.File)
		return
	}
	rf.RefactoredString = asttools.NodeToString(rf.Refactored, fset)
}

// Cleans up the refactored function by restoring the original AST function declaration, if possible.
// Does not affect the file on the disk.
func (rf *RefactoredFunction) Cleanup() {
	if rf.cleanup != nil {
		if err := rf.cleanup(); err != nil {
			slog.Error("Error cleaning up refactored function", "err", err, "function", rf.Refactored.Name.Name, "filePath", rf.FilePath)
		}
	}
}

// todo LATER - maybe add a way to unmarshal the original Refactored AST field
