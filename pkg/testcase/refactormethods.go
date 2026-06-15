package testcase

// Implementations of various test case refactoring strategies based on their analysis results.

import (
	"fmt"
	"go/token"
	"go/types"
	"log/slog"
	"os"

	"github.com/dave/dst"
	"github.com/dave/dst/dstutil"
	"github.com/maxgreen01/go-test-analyzer/pkg/asttools"
)

// Attempts to refactor a test case using the specified strategy.
// If a refactoring is successfully generated, the test is executed using the original and refactored code.
// The default behavior is to restore the original file contents after the refactoring is complete, but this
// can be disabled by setting `keepRefactoredFiles` to true.
// Saves the result of the refactoring attempt to the AnalysisResult, and also returns a copy of the result.
func (ar *AnalysisResult) AttemptRefactoring(strategy RefactorStrategy, keepRefactoredFiles bool) RefactorResult {
	if ar == nil {
		slog.Error("Attempted to refactor a nil AnalysisResult", "strategy", strategy)
		return RefactorResult{Strategy: strategy, GenerationStatus: RefactorGenerationStatusError}
	}

	// Create the RefactorResult return object and store it in the AnalysisResult
	ar.RefactorResult = RefactorResult{Strategy: strategy}
	rr := &ar.RefactorResult

	if strategy == RefactorStrategyNone {
		// Nothing to do
		return *rr
	}

	tc := ar.TestCase
	if tc == nil {
		slog.Error("Attempted to refactor a nil TestCase", "strategy", strategy)
		rr.GenerationStatus = RefactorGenerationStatusError
		return *rr
	}

	// Determine which refactoring strategy to apply
	switch strategy {
	case RefactorStrategySubtest:
		// Only refactor if the test case is table-driven and does not already use subtests
		if ar.ScenarioSet == nil || !ar.IsTableDriven() || ar.ScenarioSet.UsesSubtest {
			// Not a candidate for refactoring
			return *rr
		}

		// Perform the actual refactoring
		refactored, status, err := ar.refactorToSubtests()
		rr.GenerationStatus = status
		if err != nil {
			slog.Error("Error refactoring test case to use subtests", "err", err, "test", tc)
			return *rr
		}
		rr.Refactorings = refactored
		// Only move on to execute the test if the refactor generation step was actually successful
		if status != RefactorGenerationStatusSuccess {
			slog.Info("Could not generate subtest refactoring for test case", "status", status, "test", tc)
			return *rr
		}

	default:
		slog.Warn("Unknown refactoring strategy", "strategy", strategy)
		return *rr
	}

	//
	// If we've reached this point, the refactoring was successful and should be verified by executing the test
	//
	slog.Info("Successfully generated a refactoring for test case", "test", tc)

	// Execute the test case before saving the refactoring.
	// This is run only after refactoring succeeds to avoid running tests unnecessarily (which is quite slow).
	originalExecResult, err := tc.Execute()
	if err != nil {
		if originalExecResult == TestExecutionResultFail {
			slog.Info("Test case execution failed before refactoring", "err", err, "test", tc)
		} else {
			slog.Warn("Error executing test case before refactoring", "err", err, "test", tc)
		}
	}
	rr.OriginalExecutionResult = originalExecResult

	// Save the original contents of every affected file for later restoration, then update
	// all the files on the disk using the new refactored DST data
	originalFileContents := make(map[string][]byte)
	for _, refactoring := range rr.Refactorings {
		// Whenever this function exits, restore the original AST File data (and any dependents) to ensure that refactorings
		// don't interfere with each other. If the user requested to keep the refactored changes, skip the AST cleanup for the
		// test functions themselves so that their changes accumulate and do not get overwritten when subsequent tests in the
		// same file are saved. Always clean up helper functions to avoid interference if the helper is used in multiple tests.
		// The actual file contents is restored later in this function.
		if !keepRefactoredFiles || refactoring.IsHelper {
			defer refactoring.Cleanup()
		}

		filePath := refactoring.FilePath
		if _, ok := originalFileContents[filePath]; ok {
			// Already processed this file
			continue
		}

		// Read the entire original file contents so it can be restored after the refactoring is complete
		// todo CLEANUP - this reads the entire file into memory, which isn't ideal if multiple files need to be modified.
		//    This probably isn't a problem when files are a few MB at most, but a backup manager would be better.
		fileContents, err := os.ReadFile(filePath)
		if err != nil {
			slog.Error("Error reading original file contents", "err", err, "filePath", filePath, "test", tc)
			return *rr
		}
		originalFileContents[filePath] = fileContents

		// Update the file with the new AST data
		// Note: instead of replacing the entire file, we could possibly splice the modified function into the existing structure
		if err := asttools.SaveFileContents(filePath, refactoring.File); err != nil {
			slog.Error("Error saving refactored file", "err", err, "filePath", filePath, "test", tc)
			return *rr
		}
	}

	// Run the test after refactoring
	refactoredExecResult, err := tc.Execute()
	if err != nil {
		if refactoredExecResult == TestExecutionResultFail {
			slog.Info("Test case execution failed expectedly after refactoring", "err", err, "test", tc)
		} else {
			slog.Warn("Error executing test case after refactoring", "err", err, "test", tc)
		}
	}
	rr.RefactoredExecutionResult = refactoredExecResult
	if rr.OriginalExecutionResult != rr.RefactoredExecutionResult {
		slog.Error("Refactored test case execution results do not match original results", "original", rr.OriginalExecutionResult, "refactored", rr.RefactoredExecutionResult, "test", tc)
	}

	// Restore the original file contents on the disk to avoid refactorings interfering with each other.
	// Note that the Parser finished generating the AST structures long before this point, so the data on the disk won't
	// directly affect the underlying AST which is actually used for analysis. However, disk changes may affect test
	// execution, especially if any of the previous refactoring attempts cause compilation errors.
	if !keepRefactoredFiles {
		for _, refactoring := range rr.Refactorings {
			// Write the original file contents back to the disk
			if err := os.WriteFile(refactoring.FilePath, originalFileContents[refactoring.FilePath], 0644); err != nil {
				slog.Error("Error restoring original test file contents after refactoring", "err", err, "test", tc)
			}
		}
	}

	return *rr
}

//
// ========== Refactoring Methods ==========
//
// These may assume that the AnalysisResult has already been populated with the necessary data via `Analyze()`.
// Refactorings of helper functions are performed on *copies* of the original DST nodes to ensure that other
// analysis results are not affected if the helper is used by any other tests. The cleanup of these copy changes
// is handled by AttemptRefactoring so that they can be saved. Note that type information from `go/types` is NOT
// available for these copies since the underlying pointer values are different than the originals.
//

// TODO LATER - this DST copying behavior is only present when expanding helper statements, not necessarily when finding definitions or using the type system.
//    This is actually necessary for saving the refactoring results on disk because regular test functions are NOT reverted in the DST, which means their
//    changes are preserved between multiple refactorings, even though the same is not true for helper functions.
//    However, this can cause trouble when using `keepRefactoredFiles` because tests that cause compile errors may affect the execution of other tests in the same file.

// Refactors the test case to use subtests by wrapping the execution loop body in a call to `t.Run()`.
// Also attempts to replace `continue` statements in the Runner (except when inside another loop) with `return` to pass the test.
// Returns a one-element list containing the updated function if successful, as well as the status of the refactor
// generation attempt and any error that may have occurred.
func (ar *AnalysisResult) refactorToSubtests() ([]RefactoredFunction, RefactorGenerationStatus, error) {
	tc := ar.TestCase
	if tc == nil || tc.funcDecl == nil {
		return nil, RefactorGenerationStatusError, fmt.Errorf("cannot refactor test case that has no function declaration")
	}
	ss := ar.ScenarioSet
	if ss == nil {
		return nil, RefactorGenerationStatusError, fmt.Errorf("cannot refactor test case that is not table-driven")
	}

	// While we still have the original DST data available, get the Scope for the Runner (for use later in finding duplicate names)
	functionScope := tc.FunctionScope()
	if functionScope == nil {
		return nil, RefactorGenerationStatusError, fmt.Errorf("cannot refactor test case because function scope is not available")
	}

	// If the modified nodes are in a helper function, perform the refactoring on a copy to avoid modifying the original DST.
	// This creates the RefactoredFunction that will eventually be returned if the statement is part of a helper, because
	// the DST data it contains will be modified in-place during refactoring.
	result := cloneHelperFunction(ss.Runner, ar)

	// Extract information from the Runner loop, including saving the original values where applicable so they can be restored later
	var originalRunnerKeyName string
	var loopKeyName string
	var loopValueName string
	var originalRunnerStatements []dst.Stmt
	var runnerStatements []dst.Stmt // the code being moved into the `t.Run()` body
	switch loop := ss.Runner.(type) {
	case *dst.RangeStmt:
		// Detect the key/value variable names used by the loop (used to work with scenarios within the loop)
		if loop.Key == nil || loop.Value == nil {
			slog.Warn("Cannot refactor test case with range loop with nil key or value variable", "key", loop.Key, "value", loop.Value, "test", tc)
			return nil, RefactorGenerationStatusFail, nil
		}
		originalRunnerKeyName = loop.Key.(*dst.Ident).Name
		loopKeyName = originalRunnerKeyName
		loopValueName = loop.Value.(*dst.Ident).Name

		originalRunnerStatements = loop.Body.List
		for _, stmt := range originalRunnerStatements { // clone runner statements before modifying
			runnerStatements = append(runnerStatements, dst.Clone(stmt).(dst.Stmt))
		}

	// todo LATER add support for `for-i` loops	(and modify assignment at end of func)
	default:
		slog.Warn("Cannot refactor test case with unsupported loop type", "type", fmt.Sprintf("%T", ss.Runner), "test", tc)
		return nil, RefactorGenerationStatusFail, nil
	}

	var newStatements []dst.Stmt // the code that will be placed inside the Runner loop

	// Detect the name of the variable representing each scenario in the loop
	scenarioVarName := loopValueName // e.g. `tt` in `for _, tt := range scenarios`

	// Use the detected scenario name field, or use the first string-typed struct field if one is not detected
	nameField := ss.NameField
	if nameField == "" {
		// If the scenario is defined in a different package, we can only use exported fields
		samePackage := ss.IsScenarioFromSamePackage()

		for field := range ss.GetFields() {
			if !samePackage && !field.Exported() {
				// Skip unexported fields if the scenario is in a different package
				continue
			}
			if asttools.IsBasicType(field.Type(), types.IsString) {
				nameField = field.Name()
				break
			}
		}
	}
	if nameField == "" {
		slog.Debug("Cannot refactor test case because no valid scenario name field was detected", "test", tc)
		return nil, RefactorGenerationStatusBadFields, nil
	}

	// Create an expression representing the the scenario name, e.g. `tt.Name``
	var scenarioNameExpr dst.Expr
	if nameField == "map key" {
		// Special case where map key is used -- name is the loop key

		// If the key is ignored, replace the key with a default name so the data can be used
		if loopKeyName == "_" {
			// todo LATER - probably should make sure this name isn't already used in the function, but not a likely issue
			loopKeyName = "testName"
		}

		scenarioNameExpr = dst.NewIdent(loopKeyName)
	} else {
		// Regular case -- name is a scenario field
		scenarioNameExpr = asttools.NewSelectorExpr(scenarioVarName, nameField)
	}

	// Safety case: if the scenario type is a pointer, add a nil check before accessing the name field.
	// The generated nil check has the form:
	// ```
	// var name string
	// if {scenarioVarName} != nil {
	//     name = {scenarioNameExpr}
	// } else {
	//     name = "nil"
	// }
	// ```
	if asttools.IsPointer(ss.ScenarioType) {
		// Construct the new nodes
		// Note that any reused DST nodes need to be cloned to ensure they are unique, or else this will cause a panic
		scenarioNameVarName := asttools.GenerateUniqueName(functionScope, "name")
		assignNameTo := func(rhs dst.Expr) dst.Stmt {
			return &dst.AssignStmt{Lhs: []dst.Expr{dst.NewIdent(scenarioNameVarName)}, Tok: token.ASSIGN, Rhs: []dst.Expr{rhs}}
		}
		newStatements = append(newStatements, &dst.DeclStmt{
			// ```var name string```
			Decl: &dst.GenDecl{
				Tok:   token.VAR,
				Specs: []dst.Spec{&dst.ValueSpec{Names: []*dst.Ident{dst.NewIdent(scenarioNameVarName)}, Type: dst.NewIdent("string")}},
			},
		})
		newStatements = append(newStatements, &dst.IfStmt{
			// ```if {scenarioVarName} != nil```
			Cond: &dst.BinaryExpr{X: dst.NewIdent(scenarioVarName), Op: token.NEQ, Y: dst.NewIdent("nil")},
			// ```name = {scenarioNameExpr}```
			Body: &dst.BlockStmt{List: []dst.Stmt{assignNameTo(scenarioNameExpr)}},
			// ```name = "nil"```
			Else: &dst.BlockStmt{List: []dst.Stmt{assignNameTo(&dst.BasicLit{Kind: token.STRING, Value: "\"nil\""})}},
		})
		// Use the new `name` variable as the subtest name
		scenarioNameExpr = dst.NewIdent(scenarioNameVarName)
	}

	// Detect the name of the `*testing.T` parameter in the Runner's function body, instead of hardcoding it to "t"
	funcDecl, _ := asttools.GetEnclosingFunction(tc.DstStartPos(ss.Runner), tc.GetPackageFiles())
	if funcDecl == nil || funcDecl.Type == nil {
		return nil, RefactorGenerationStatusError, fmt.Errorf("cannot refactor test case with missing function declaration")
	}
	// Look for either `*testing.T` or `*require.TestingT`
	tVarName, err := asttools.GetParamNameByType(tc.AstToDst(funcDecl).(*dst.FuncDecl), &dst.StarExpr{X: asttools.NewSelectorExpr("testing", "T")}, &dst.StarExpr{X: asttools.NewSelectorExpr("require", "TestingT")})
	if err != nil {
		slog.Warn("Cannot refactor test case because a `*testing.T` parameter was not detected", "err", err, "function", funcDecl.Name.Name, "test", tc)
		return nil, RefactorGenerationStatusNoTester, nil
	}

	// ENHANCEMENT
	// To hopefully avoid compilation errors, try to replace `continue` runnerStatements in the loop body with `return` to make the test pass.
	for _, stmt := range runnerStatements {
		// Detect continue statements without a label, except when inside another loop
		dstutil.Apply(stmt, func(c *dstutil.Cursor) bool {
			n := c.Node()
			switch x := n.(type) {
			case *dst.RangeStmt, *dst.ForStmt:
				// Don't inspect internal loops because they're a valid place for more continue statements
				return false
			case *dst.BranchStmt:
				// Only replace continue statements without a label
				// todo LATER - can't handle nested loop that continue the main runner because we don't know if the runner is labeled
				if x.Tok == token.CONTINUE && x.Label == nil {
					// c.Replace(asttools.NewCallExprStmtDST(asttools.NewSelectorExprDST(tVarName, "Skip"), nil))
					c.Replace(&dst.ReturnStmt{})
				}
			}
			return true
		}, nil)
	}

	// Construct the actual `t.Run()` call statement using all the data we have so far
	tRunCall := asttools.NewCallExprStmt(
		asttools.NewSelectorExpr(tVarName, "Run"),
		[]dst.Expr{
			// Scenario name, like `tt.Name`
			scenarioNameExpr,

			// Function literal for the test body, of form `func(t *testing.T) { ... }`
			&dst.FuncLit{
				Type: &dst.FuncType{
					// The `*testing.T` parameter
					Params: &dst.FieldList{
						List: []*dst.Field{
							{
								Names: []*dst.Ident{
									dst.NewIdent(tVarName),
								},
								Type: &dst.StarExpr{
									X: asttools.NewSelectorExpr("testing", "T"),
								},
							},
						},
					},
				},
				// The function body, populated with the original loop body statements
				Body: &dst.BlockStmt{
					List: runnerStatements,
				},
			},
		},
	) // end of constructing `t.Run()` call
	newStatements = append(newStatements, tRunCall)

	// At this point, the refactored DST changes are mostly complete, but we haven't been applied yet

	// Create a closure to restore the original Runner data once all refactoring is done
	cleanupFunc := func() error {
		slog.Debug("Restoring original scenario Runner", "test", tc)
		switch loop := ss.Runner.(type) {
		case *dst.RangeStmt:
			loop.Body.List = originalRunnerStatements
			loop.Key.(*dst.Ident).Name = originalRunnerKeyName
		}
		return nil
	}

	// Apply the refactoring changes to the underlying DST now that the refactoring logic is complete
	switch loop := ss.Runner.(type) {
	case *dst.RangeStmt:
		loop.Body.List = newStatements
		// If the range key identifier changed, update that too
		if loopKeyName != loop.Key.(*dst.Ident).Name {
			loop.Key.(*dst.Ident).Name = loopKeyName
		}

		// unsupported loop types are handled above
	}

	// If `result` is non-nil, the statement was part of a helper function and the refactored data should already be
	// contained within this struct. However, the string representation of the refactored function needs to be updated.
	// Note that the new `cleanupFunc` has a smaller scope than the existing one inside `result` because it only restores
	// the Runner data rather than the entire original function, so we can just rely on the existing one.
	if result != nil {
		result.UpdateStringRepresentation()
		return []RefactoredFunction{*result}, RefactorGenerationStatusSuccess, nil
	}

	// Either the statement is not part of a helper function (or an error occurred while checking for that),
	// so we assume that the refactoring happened inside the original test function and doesn't need any separate DST cleanup.
	return []RefactoredFunction{*NewRefactoredFunction(tc.funcDecl, tc.file, tc.FilePath, false, cleanupFunc)}, RefactorGenerationStatusSuccess, nil
}

//
// ========== Helper Functions ==========
//

// If the provided statement is part of a helper function (i.e. not the test case function itself), this replaces
// the surrounding helper function with a deep copy of itself in the included TestCase's DST file. It also updates
// the DST references in the included ScenarioSet in-place to use the cloned data. This returns a representation of
// the refactored function, where the Refactored field is the unmodified copy of the original function declaration.
//
// If the statement is not part of a helper function or is not part of the package, this does nothing.
func cloneHelperFunction(stmt dst.Stmt, ar *AnalysisResult) *RefactoredFunction {
	// Assumed to be non-nil by this point
	tc := ar.TestCase
	ss := ar.ScenarioSet

	originalAstFunc, enclosingAstFile := asttools.GetEnclosingFunction(tc.DstStartPos(stmt), tc.GetPackageFiles())

	if originalAstFunc == nil || enclosingAstFile == nil {
		slog.Warn("Tried processing a statement that is not part of a function in the package", "statement", fmt.Sprintf("%T", stmt), "test", tc)
		return nil
	}
	fset := tc.FileSet()
	if fset == nil {
		slog.Warn("Cannot determine path to file enclosing a helper function because FileSet is nil", "function", originalAstFunc.Name.Name, "test", tc)
		return nil
	}

	// Convert data to DST before proceeding
	originalFunc, okFunc := tc.AstToDst(originalAstFunc).(*dst.FuncDecl)
	enclosingFile, okFile := tc.AstToDst(enclosingAstFile).(*dst.File)
	if !okFunc || !okFile {
		slog.Warn("Could not convert surrounding AST nodes to DST", "statement", fmt.Sprintf("%T", stmt), "test", tc)
		return nil
	}

	if originalFunc.Name.Name == tc.funcDecl.Name.Name && enclosingFile.Name.Name == tc.PackageName {
		// Statement is part of the test case function itself, so no need to clone it
		slog.Debug("Statement is part of the test case function itself", "statement", fmt.Sprintf("%T", stmt), "function", originalFunc.Name.Name, "test", tc)
		return nil
	}
	slog.Debug("Statement is part of a helper function", "statement", fmt.Sprintf("%T", stmt), "function", originalFunc.Name.Name, "test", tc)

	// Create a deep copy of the enclosing function to avoid modifying the original DST data
	copiedFunc := dst.Clone(originalFunc).(*dst.FuncDecl)

	// Replace the original function declaration with its copy within the DST file
	if err := asttools.ReplaceFuncDecl(originalFunc, copiedFunc, enclosingFile); err != nil {
		slog.Error("Failed to replace function declaration with its copy", "err", err, "test", tc)
		return nil
	}
	// Create a closure to restore the original function declaration within the DST file
	restoreFuncDecl := func() error {
		if err := asttools.ReplaceFuncDecl(copiedFunc, originalFunc, enclosingFile); err != nil {
			return fmt.Errorf("restoring original function declaration: %w", err)
		}
		return nil
	}

	// Now that the copied data is spliced into the file, update the DST references in the ScenarioSet to use the corresponding copied statements
	originalRunner := ss.Runner // Save a copy of the original reference so it can be restored later
	copiedRunner, err := asttools.GetStmtWithSameIndex(ss.Runner, originalFunc.Body.List, copiedFunc.Body.List)
	if err != nil {
		slog.Error("Failed to update ScenarioSet runner statement reference", "err", err, "function", originalFunc.Name.Name, "test", tc)
		// If something went wrong, we need to restore the original function declaration to bring everything back to the original state
		if err := restoreFuncDecl(); err != nil {
			slog.Error("Failed to restore original function declaration", "err", err)
		}
		return nil
	}
	ss.Runner = copiedRunner

	// Create a closure to restore the original function declaration and all DST ScenarioSet references once all refactoring is done
	cleanupFunc := func() error {
		slog.Debug("Restoring original helper function declaration", "test", tc)
		if err := restoreFuncDecl(); err != nil {
			return err
		}
		ss.Runner = originalRunner
		return nil
	}

	return NewRefactoredFunction(copiedFunc, enclosingFile, fset.Position(enclosingAstFile.FileStart).Filename, true, cleanupFunc)
}
