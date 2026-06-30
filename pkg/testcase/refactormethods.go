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
	"github.com/maxgreen01/go-test-analyzer/pkg/dstequal"
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
		if refactoredExecResult == TestExecutionResultFail && originalExecResult == TestExecutionResultFail {
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
// All refactoring is performed on a *copy* of the original DST function so that the refactored code can easily
// be reverted to avoid affecting the analysis results of other tests. The restoration of the original DST data
// is handled in `AttemptRefactoring()` so that the refactored code can be tested before being reverted.
//
// Note that type information from `go/types` and original AST information (including position) are NOT available
// during refactoring because the cloned DST data has different underlying pointer values than the original nodes.
// Since ExpandedStatement logic relies on both of these, and since (typically) only the function being refactored
// is cloned, this should also be avoided without special care. Scope information is likewise not available for
// the cloned DST data, but the original function's scope information is usually sufficient for avoiding variable
// names that already exist.
//

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
	functionScope := tc.GetFunctionScope()
	if functionScope == nil {
		return nil, RefactorGenerationStatusError, fmt.Errorf("cannot refactor test case because function scope is not available")
	}

	// Always perform the refactoring on a copy of the function data to avoid modifying the original DST.
	// This creates the RefactoredFunction that will eventually be returned from this function, because
	// the DST data it contains will be modified in-place during refactoring.
	result := cloneSurroundingFunction(ss.Runner, ar)
	if result == nil {
		return nil, RefactorGenerationStatusError, fmt.Errorf("failed to clone enclosing function before refactoring")
	}

	// Use the detected scenario name field as the subtest label, or use the first string-typed struct field if one is not detected
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

	// Extract information from the Runner loop, and set up the key/value/index variables and scenario expression for later.
	// This includes modifying variables declared in the Runner loop itself, if needed.
	scenarioStructName := ss.ScenarioStructName
	var loopIndexName string        // the name of the variable representing the loop index
	var scenarioExpr dst.Expr       // the expression representing the scenario being processed, e.g. `scenarios[i]` or `tt` in `for _, tt := range scenarios`
	var runnerStatements []dst.Stmt // the code being moved into the `t.Run()` body

	switch loop := ss.Runner.(type) {
	case *dst.RangeStmt:
		// Detect the key (index) variable defined by by the range loop
		if loop.Key != nil {
			keyIdent, ok := loop.Key.(*dst.Ident)
			if !ok {
				slog.Warn("Cannot refactor range loop with non-identifier key", "key", loop.Key, "test", tc)
				return nil, RefactorGenerationStatusFail, nil
			}
			loopIndexName = keyIdent.Name
		}

		// Detect the value (scenario) variable defined by the range loop
		var loopValueName string
		if loop.Value != nil {
			valueIdent, ok := loop.Value.(*dst.Ident)
			if !ok {
				slog.Warn("Cannot refactor range loop with non-identifier value", "value", loop.Value, "test", tc)
				return nil, RefactorGenerationStatusFail, nil
			}
			loopValueName = valueIdent.Name
		}

		// Try to access the scenario using the loop value by default. If the value variable is nil, use indexing to access the scenario variable instead.
		// Note: it's possible to add a new loop value variable, but this can't be done if it's a range over a non-scenario type (e.g. an int).
		// To avoid extra complexity with detecting the type of the range expression, only generate a new value variable if it's already defined but blank.
		accessScenarioByValue := loopValueName != ""

		// If we're accessing the scenario by index, or the loop key is the scenario name, we need to use the loop's key variable
		needLoopKey := !accessScenarioByValue || nameField == "map key"

		// Generate a new index/key variable name if it's needed but not already defined
		if needLoopKey && (loopIndexName == "" || loopIndexName == "_") {
			keyName := "i"
			if nameField == "map key" {
				keyName = "key"
			}
			loopIndexName = asttools.GenerateUniqueName(keyName, functionScope)
		}

		// Create an expression to represent the scenario based on the loop's value or index
		if accessScenarioByValue {
			// Access scenario by value, generating a new variable name if the existing one is blank
			if loopValueName == "_" {
				loopValueName = asttools.GenerateUniqueName("scenario", functionScope)
			}
			// Also make sure the key variable exists in case it wasn't already defined
			if loopIndexName == "" {
				loopIndexName = "_"
			}
			scenarioExpr = dst.NewIdent(loopValueName)

		} else {
			// Access scenario using an index (the loop key)
			if scenarioStructName == "" {
				slog.Warn("Cannot refactor range loop without value variable because scenario structure variable name is not known", "test", tc)
				return nil, RefactorGenerationStatusFail, nil
			}
			scenarioExpr = &dst.IndexExpr{
				X:     dst.NewIdent(scenarioStructName),
				Index: dst.NewIdent(loopIndexName),
			}
		}

		// Update loop key variable if it changed
		if loop.Key == nil {
			// Define a new variable entirely
			loop.Key = dst.NewIdent(loopIndexName)
			loop.Tok = token.DEFINE
		} else if keyIdent, ok := loop.Key.(*dst.Ident); ok && keyIdent.Name != loopIndexName {
			// Update the existing variable
			loop.Key.(*dst.Ident).Name = loopIndexName
		}

		// Update loop value variable if it was added or changed
		if loopValueName != "" {
			if loop.Value == nil {
				// Define a new variable entirely
				loop.Value = dst.NewIdent(loopValueName)
				loop.Tok = token.DEFINE
			} else if valueIdent, ok := loop.Value.(*dst.Ident); ok && valueIdent.Name != loopValueName {
				// Update the existing variable
				loop.Value.(*dst.Ident).Name = loopValueName
			}
		}

		runnerStatements = loop.Body.List

	case *dst.ForStmt:
		// Try to access the scenario using the loop's index variable
		if scenarioStructName == "" {
			slog.Warn("Cannot refactor index loop because scenario structure variable name is not known", "test", tc)
			return nil, RefactorGenerationStatusFail, nil
		}

		// If the index variable is blank or not defined, don't attempt to generate a new variable because there's a high risk of breaking the existing functionality
		if indexIdent := GetForStmtIndexIdent(loop); indexIdent != nil {
			loopIndexName = indexIdent.Name
		}
		if loopIndexName == "" || loopIndexName == "_" {
			slog.Warn("Cannot refactor index loop because index variable is not defined or is blank", "test", tc)
			return nil, RefactorGenerationStatusFail, nil
		}

		scenarioExpr = &dst.IndexExpr{
			X:     dst.NewIdent(scenarioStructName),
			Index: dst.NewIdent(loopIndexName),
		}

		runnerStatements = loop.Body.List

	default:
		slog.Warn("Cannot refactor test case with unsupported loop type", "type", fmt.Sprintf("%T", ss.Runner), "test", tc)
		return nil, RefactorGenerationStatusFail, nil
	}

	var newStatements []dst.Stmt // the new code that will be placed inside the Runner loop

	// Create an expression representing the scenario name, e.g. `tt.Name` or `scenarios[i].Name`
	var scenarioNameExpr dst.Expr
	if nameField == "map key" {
		// Special case where map key is used -- name is the loop key
		scenarioNameExpr = dst.NewIdent(loopIndexName)
	} else {
		// Regular case -- name is a scenario field
		scenarioNameExpr = &dst.SelectorExpr{
			X:   dst.Clone(scenarioExpr).(dst.Expr),
			Sel: dst.NewIdent(nameField),
		}
	}

	// Safety case: if the scenario type is a pointer, add a nil check before accessing the name field.
	// The generated nil check has the form:
	// ```
	// var name string
	// if {scenarioExpr} != nil {
	//     name = {scenarioNameExpr}
	// } else {
	//     name = "nil"
	// }
	// ```
	if asttools.IsPointer(ss.ScenarioType) {
		// Construct the new nodes
		// Note that any reused DST nodes need to be cloned to ensure they are unique, or else this will cause a panic
		scenarioNameVarName := asttools.GenerateUniqueName("name", functionScope)
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
			// ```if {scenarioExpr} != nil```
			Cond: &dst.BinaryExpr{X: dst.Clone(scenarioExpr).(dst.Expr), Op: token.NEQ, Y: dst.NewIdent("nil")},
			// ```name = {scenarioNameExpr}```
			Body: &dst.BlockStmt{List: []dst.Stmt{assignNameTo(scenarioNameExpr)}},
			// ```name = "nil"```
			Else: &dst.BlockStmt{List: []dst.Stmt{assignNameTo(&dst.BasicLit{Kind: token.STRING, Value: "\"nil\""})}},
		})
		// Use the new `name` variable as the subtest name
		scenarioNameExpr = dst.NewIdent(scenarioNameVarName)
	}

	// Detect the name of the `*testing.T` parameter in the Runner's function body, instead of hardcoding it to "t"
	funcDecl := result.Refactored
	if funcDecl == nil || funcDecl.Type == nil {
		return nil, RefactorGenerationStatusError, fmt.Errorf("cannot refactor test case with missing function declaration")
	}
	tVarName, err := GetTestingParamName(tc.funcDecl)
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

	// Apply the refactoring changes to the underlying DST now that the refactoring logic is complete.
	// Unsupported loop types are rejected at the start of this function, so we don't have to check here.
	switch loop := ss.Runner.(type) {
	case *dst.RangeStmt:
		loop.Body.List = newStatements
	case *dst.ForStmt:
		loop.Body.List = newStatements
	}

	// Since the original DST function was cloned earlier, the refactored data should already be contained within the result struct.
	// However, the string representation of the refactored function needs to be updated now that the refactoring is finished.
	result.UpdateStringRepresentation()
	return []RefactoredFunction{*result}, RefactorGenerationStatusSuccess, nil
}

//
// ========== Helper Functions ==========
//

// Performs a deep copy of the function surrounding the provided statement, then modifies the DST file of the
// AnalysisResult's TestCase in-place to replace the original function with the copy. This also updates the DST
// references in the AnalysisResult's ScenarioSet and TestCase in-place to use the cloned data. The provided
// statement must not be a clone, or else this will fail. Returns a representation of the cloned function, where
// the `Refactored` field is the (so far) unmodified copy of the original function declaration.
func cloneSurroundingFunction(stmt dst.Stmt, ar *AnalysisResult) *RefactoredFunction {
	// Assumed to be non-nil by this point
	tc := ar.TestCase
	ss := ar.ScenarioSet

	originalAstFunc, enclosingAstFile := asttools.GetEnclosingFunction(tc.DstStartPos(stmt), tc.GetPackageFiles())

	if originalAstFunc == nil || enclosingAstFile == nil {
		slog.Warn("Cannot clone the function surrounding a statement that is not in the test's package", "statement", fmt.Sprintf("%T", stmt), "test", tc)
		return nil
	}
	fset := tc.FileSet()
	if fset == nil {
		slog.Warn("Cannot clone a function because FileSet is nil", "function", originalAstFunc.Name.Name, "test", tc)
		return nil
	}

	// Convert data to DST before proceeding
	originalFunc, okFunc := tc.AstToDst(originalAstFunc).(*dst.FuncDecl)
	enclosingFile, okFile := tc.AstToDst(enclosingAstFile).(*dst.File)
	if !okFunc || !okFile {
		slog.Warn("Could not clone a function because the surrounding AST nodes could not be converted to DST", "function", originalAstFunc.Name.Name, "statement", fmt.Sprintf("%T", stmt), "test", tc)
		return nil
	}

	// Determine if the statement is part of a helper function or the test function itself
	isHelper := !dstequal.Decl(originalFunc, tc.funcDecl)
	slog.Debug("Cloning function before refactoring", "statement", fmt.Sprintf("%T", stmt), "function", originalAstFunc.Name.Name, "isHelper", isHelper, "test", tc)

	// Create a deep copy of the enclosing function to avoid modifying the original DST data
	copiedFunc := dst.Clone(originalFunc).(*dst.FuncDecl)

	// Replace the original function declaration with its copy within the DST file
	if err := asttools.ReplaceFuncDecl(originalFunc, copiedFunc, enclosingFile); err != nil {
		slog.Error("Failed to replace function declaration with its copy", "err", err, "test", tc)
		return nil
	}
	if !isHelper {
		// The original function is already saved in `originalFunc` according to the `isHelper` definition
		tc.funcDecl = copiedFunc
	}

	// Create a closure to restore the original function declaration within the DST file
	restoreFuncDecl := func() error {
		if err := asttools.ReplaceFuncDecl(copiedFunc, originalFunc, enclosingFile); err != nil {
			return fmt.Errorf("restoring original function declaration: %w", err)
		}
		if !isHelper {
			tc.funcDecl = originalFunc
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
		slog.Debug("Restoring original function declaration", "function", originalFunc.Name.Name, "test", tc)
		if err := restoreFuncDecl(); err != nil {
			return err
		}
		ss.Runner = originalRunner
		return nil
	}

	return NewRefactoredFunction(copiedFunc, enclosingFile, fset.Position(enclosingAstFile.FileStart).Filename, isHelper, cleanupFunc)
}
