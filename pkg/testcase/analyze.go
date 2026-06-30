package testcase

import (
	"go/token"
	"go/types"
	"log/slog"
	"slices"

	"github.com/dave/dst"
	"github.com/maxgreen01/go-test-analyzer/pkg/asttools"
	"github.com/maxgreen01/go-test-analyzer/pkg/dstequal"
)

// Attempts to extract the table-driven properties of a test case using information extracted from its parsed statements
func IdentifyScenarioSet(tc *TestCase, statements []*ExpandedStatement) *ScenarioSet {
	if tc == nil {
		slog.Error("Cannot identify Scenarios in nil TestCase")
		return nil
	}
	if len(statements) == 0 {
		slog.Warn("Cannot identify ScenarioSet because there are no statements", "testCase", tc)
		return nil
	}

	// Initialize the TestCase's ScenarioSet, whose fields will be populated throughout this method with relevant data
	ss := &ScenarioSet{TestCase: tc}

	// Iterate test statements in reverse to find the runner loop before trying to find the scenarios
outerStmtLoop:
	for _, expanded := range slices.Backward(statements) {
		if expanded == nil {
			slog.Warn("Encountered nil statement in test case", "testCase", tc)
			continue outerStmtLoop
		}

		// Extract the loop that runs the subtests, which should not be part of a helper function (to reduce falsely identified table-driven tests)
		// todo NOTE - to allow subtest runners inside helper functions, move this block inside the loop over `expanded.All()`
		if ss.Runner == nil {
			stmt := expanded.Stmt
			// Detect the loop itself
			if rangeStmt, ok := stmt.(*dst.RangeStmt); ok {
				// slog.Debug("Found range statement in test case", "testCase", tc.TestName)

				// Detect if the loop ranges over a valid data structure directly, e.g. `for _, s := range scenarios`
				ss.detectScenarioDataStructure(rangeStmt.X)

				// Found range over scenario data structure directly - easy case
				if ss.DataStructure != ScenarioNoDS {
					// Check if the scenario data structure is defined directly in the range statement
					if _, ok := rangeStmt.X.(*dst.CompositeLit); ok {
						if ss.IdentifyScenarios(rangeStmt.X, tc) {
							slog.Debug("Found scenario definition directly in the range statement", "testCase", tc, "scenarios", len(ss.Scenarios))
						}
					}
					ss.Runner = rangeStmt
					continue outerStmtLoop // Move to the next test statement
				}

				// Check if the loop ranges over an integer that's used for accessing scenarios, e.g. `for i := range len(scenarios)`
				// It's technically possible to do this without defining a range key variable, but that structure is intentionally not detected to limit false detections.
				slog.Debug("Checking for range over int loop to access scenarios", "testCase", tc)
				if rangeStmt.Key != nil {
					// Detect if the loop ranges over `len(scenarios)`
					ss.detectScenariosInLen(rangeStmt.X)
					// todo probably should enforce the read-only index requirements even if scenarios are found in the condition, or maybe just don't check for len() directly?

					// If a data structure is still not found, search the loop body based on the index variable
					if ss.DataStructure == ScenarioNoDS {
						var indexVarName string
						if rangeKey, ok := rangeStmt.Key.(*dst.Ident); ok {
							indexVarName = rangeKey.Name
						}
						ss.detectScenariosByIndex(rangeStmt.Body, indexVarName)
					}
				}

				// If we still haven't found a valid scenario data structure, try analyzing a different statement
				if ss.DataStructure == ScenarioNoDS {
					slog.Debug("Detected a range loop in test case, but didn't find a valid scenario structure", "testCase", tc)
					continue outerStmtLoop
				}

				ss.Runner = rangeStmt

			} else if forStmt, ok := stmt.(*dst.ForStmt); ok {
				// slog.Debug("Found index loop statement in test case", "testCase", tc.TestName)

				// Detect if the loop uses `len(scenarios)` in the condition (assuming a binary comparison), e.g. `for i := 0; i < len(scenarios); i++`
				if cond, ok := forStmt.Cond.(*dst.BinaryExpr); ok {
					ss.detectScenariosInLen(cond.Y)
					if ss.DataStructure == ScenarioNoDS {
						ss.detectScenariosInLen(cond.X)
					}
				}
				// todo probably should enforce the read-only index requirements even if scenarios are found in the condition, or maybe just don't check for len() directly?

				// If a data structure is still not found, search the loop body based on the index variable
				if ss.DataStructure == ScenarioNoDS {
					if indexIdent := GetForStmtIndexIdent(forStmt); indexIdent != nil {
						ss.detectScenariosByIndex(forStmt.Body, indexIdent.Name)
					}
				}

				// If we still haven't found a valid scenario data structure, try analyzing a different statement
				if ss.DataStructure == ScenarioNoDS {
					slog.Debug("Detected an index loop in test case, but didn't find a valid scenario structure", "testCase", tc)
					continue outerStmtLoop
				}

				ss.Runner = forStmt
			}
			continue outerStmtLoop // Move to the next test statement
		} // end of check for Runner loop

		// Iterate over each component of the expanded statement, i.e. look into expanded helper functions
		for stmt := range expanded.All() {

			// Search for variable assignments matching the detected scenario data structure, with the goal of finding the scenario definitions.
			// Note that `ScenarioStructName` is not modified here since these definitions might be inside helper functions and use a different name than the test itself.
			if ss.Scenarios == nil && ss.ScenarioType != nil {
				switch assignment := stmt.(type) {
				case *dst.AssignStmt:
					// Statements like `scenarios := []Scenario{...}`
					for _, expr := range assignment.Rhs {
						found := ss.IdentifyScenarios(expr, tc)
						if found {
							slog.Debug("Found scenario definition in function body", "testCase", tc, "scenarios", len(ss.Scenarios))
							continue outerStmtLoop // Move to the next test statement
						}
					}
				case *dst.DeclStmt:
					// Statements like `var scenarios = []Scenario{...}`
					if ss.identifyScenariosFromDecl(assignment.Decl) {
						slog.Debug("Found scenario definition in function body", "testCase", tc, "scenarios", len(ss.Scenarios))
						continue outerStmtLoop // Move to the next test statement
					}
				}
			}
		} // end of loop over expanded statement components
	} // end of loop over expanded statements

	// If the loop was found but the Scenario definitions were not, check the file declarations in case they were defined outside the function
	if ss.Scenarios == nil && ss.ScenarioType != nil {
		slog.Debug("No scenarios found in the test case, checking file declarations", "testCase", tc)

		if tc.GetFile() == nil {
			slog.Error("Cannot check file declarations because File is nil", "testCase", tc)
		} else {
			for _, decl := range tc.GetFile().Decls {
				if ss.identifyScenariosFromDecl(decl) {
					slog.Debug("Found scenario definition in file declarations", "testCase", tc, "scenarios", len(ss.Scenarios))
					break // Stop checking file declarations
				}
			}
		}
	} // end of check for scenarios in file declarations

	// Attempt to perform additional analysis on the ScenarioSet
	ss.Analyze()
	return ss
}

// Checks if the provided expression represents a data structure used to store scenarios in a
// table-driven test based on the underlying data type (usually a struct) used to define scenarios.
// Also checks if the key of a map structure is used to define each scenario's name.
//
// Saves the detected ScenarioDataStructure category, the original (not underlying) type used to
// define each scenario, and the name of the variable that holds the scenarios (if the provided
// expression is an identifier) to the `ScenarioSet`.
func (ss *ScenarioSet) detectScenarioDataStructure(expr dst.Expr) {
	typ := ss.TestCase.TypeOf(expr)
	if typ == nil {
		return
	}

	// To avoid interference with running this multiple times, reset the relevant ScenarioSet fields before proceeding
	ss.DataStructure = ScenarioNoDS
	ss.ScenarioType = nil
	ss.ScenarioStructName = ""

	// Check the underlying type of the whole data structure
	switch x := typ.Underlying().(type) {

	case *types.Slice, *types.Array:
		// Check for []struct or [N]struct
		elemType := x.(asttools.Elemer).Elem()
		if _, ok := asttools.UnderlyingType(elemType).(*types.Struct); ok {
			ss.DataStructure, ss.ScenarioType = ScenarioStructListDS, elemType // save the original data type, not the underlying one
		}

	case *types.Map:
		// Check for map[any]any
		// map[any]struct is expected most of the time, but something like map[string]bool is fine too
		ss.DataStructure = ScenarioMapDS
		ss.ScenarioType = x.Elem()

		// If the map key is a string (not considering underlying type), assume it's the scenario name
		if asttools.IsBasicType(x.Key(), types.IsString) {
			ss.NameField = "map key"
		}
	default:
		// Not a recognized data structure type
		return
	}

	// If this is a recognized scenario data structure, the provided expression represents the scenarios themselves.
	// This name could be empty if scenarios are defined directly in a range statement (e.g. as a CompositeLit or CallExpr) without being
	// assigned to a separate variable, but this means the range statement should already have a value expression to reference the current scenario.
	if ident, ok := expr.(*dst.Ident); ss.DataStructure != ScenarioNoDS && ok {
		ss.ScenarioStructName = ident.Name
	}
}

// Checks whether an expression has the same original (not underlying) type as the ScenarioType, and if so, saves the scenarios from the expression.
// Returns whether the scenarios were saved successfully. Always returns `false` if the `ScenarioSet.DataStructure` is unknown.
// See https://go.dev/ref/spec#Type_identity for details of the `types.Identical` comparison method.
func (ss *ScenarioSet) IdentifyScenarios(expr dst.Expr, tc *TestCase) bool {
	if tc == nil {
		slog.Error("Cannot identify Scenarios in nil TestCase")
		return false
	}

	// Both []struct and map are defined using a CompositeLit, so make sure this matches
	if compositeLit, ok := expr.(*dst.CompositeLit); ok {
		if len(compositeLit.Elts) == 0 {
			return false
		}

		// Depending on the scenario data structure, extract and save the scenarios themselves
		// todo LATER construct Scenario structs inside the cases.    also might have to make changes here to handle non-struct fields
		switch ss.DataStructure {

		case ScenarioStructListDS:
			// Scenarios are directly stored as the elements of the slice
			typ := tc.TypeOf(compositeLit.Elts[0])
			if typ != nil && types.Identical(typ, ss.ScenarioType) {
				ss.Scenarios = compositeLit.Elts
				return true
			}

		case ScenarioMapDS:
			// Scenarios are stored as the values of the `KeyValueExpr` elements
			kvExpr, ok := compositeLit.Elts[0].(*dst.KeyValueExpr)
			if !ok {
				return false
			}
			typ := tc.TypeOf(kvExpr.Value)
			if typ != nil && types.Identical(typ, ss.ScenarioType) {
				for _, elt := range compositeLit.Elts {
					if kvExpr, ok := elt.(*dst.KeyValueExpr); ok {
						ss.Scenarios = append(ss.Scenarios, kvExpr)
					}
				}
				return true
			}
		}
	}
	return false
}

// Returns the identifier representing the index variable of a non-range loop by finding the first variable
// to be modified in the loop's initialization or post iteration expression. Returns nil if no suitable
// identifier is found.
func GetForStmtIndexIdent(loop *dst.ForStmt) *dst.Ident {
	// Check the init statement
	switch init := loop.Init.(type) {
	case *dst.AssignStmt:
		if len(init.Lhs) > 0 {
			if ident, ok := init.Lhs[0].(*dst.Ident); ok {
				return ident
			}
		}
	case *dst.DeclStmt:
		if genDecl, ok := init.Decl.(*dst.GenDecl); ok {
			for _, spec := range genDecl.Specs {
				if valueSpec, ok := spec.(*dst.ValueSpec); ok && len(valueSpec.Names) > 0 {
					return valueSpec.Names[0]
				}
			}
		}
	}
	// If there's no init statement, check the post iteration statement
	switch post := loop.Post.(type) {
	case *dst.IncDecStmt:
		if ident, ok := post.X.(*dst.Ident); ok {
			return ident
		}
	case *dst.AssignStmt:
		if len(post.Lhs) > 0 {
			if ident, ok := post.Lhs[0].(*dst.Ident); ok {
				return ident
			}
		}
	}
	return nil
}

// Checks if an expression is ever used on the LHS of an assignment or increment/decrement within the given body.
// todo LATER maybe this could be checked using ExpandedStatement too, but it would be hard to track args being passed since the name could change
func isAssigned(target dst.Expr, body []dst.Stmt) bool {
	assigned := false
	for _, stmt := range body {
		// Check for the matching expression on the LHS of an assignment or increment/decrement statement
		for _, lhs := range asttools.FindModifiedExpressions(stmt) {
			dst.Inspect(lhs, func(child dst.Node) bool {
				if dstequal.Node(child, target) {
					assigned = true
					return false // Stop descending if a matching assignment has been found
				}
				return true
			})
			if assigned {
				return true
			}
		}
	}
	return false
}

// Checks if the expression is a call to `len(x)`, where x represents a valid scenario data structure
// based on `detectScenarioDataStructure()`. Saves the data structure to the `ScenarioSet` if found.
func (ss *ScenarioSet) detectScenariosInLen(expr dst.Expr) {
	if callExpr, ok := expr.(*dst.CallExpr); ok {
		if ident, ok := callExpr.Fun.(*dst.Ident); ok && ident.Name == "len" && len(callExpr.Args) > 0 {
			arg := callExpr.Args[0]
			ss.detectScenarioDataStructure(arg)
		}
	}
}

// Searches through a block statement to find an index statement on a valid scenario data structure
// based on `detectScenarioDataStructure()` using the given index variable name. The index expression
// must never appear on the LHS of an assignment, since scenario data should be read-only. Saves the
// data structure to the `ScenarioSet` if found.
func (ss *ScenarioSet) detectScenariosByIndex(body *dst.BlockStmt, indexVarName string) {
	if indexVarName == "" || indexVarName == "_" || body == nil {
		return
	}
	dst.Inspect(body, func(n dst.Node) bool {
		// Stop iterating if we've already found a valid scenario data structure
		if ss.DataStructure != ScenarioNoDS {
			return false
		}

		// Index expression like `container[indexVarName]`
		if indexExpr, ok := n.(*dst.IndexExpr); ok {
			if indexIdent, ok := indexExpr.Index.(*dst.Ident); ok && indexIdent.Name == indexVarName {
				if containerIdent, ok := indexExpr.X.(*dst.Ident); ok {
					// Ignore this expression if the variable is ever on the LHS of assignment in the body (must be read-only)
					if isAssigned(containerIdent, body.List) {
						return true
					}
					// Try to detect scenario data structure of the container
					ss.detectScenarioDataStructure(containerIdent)
					if ss.DataStructure != ScenarioNoDS {
						return false // Stop descending
					}
				}
			}
		}
		return true
	})
}

// Searches through a declaration for variable assignments matching the detected scenario data structure
// using `IdentifyScenarios()`. Returns true and saves the scenarios to the `ScenarioSet` if a matching
// assignment is found.
func (ss *ScenarioSet) identifyScenariosFromDecl(decl dst.Decl) bool {
	if decl == nil {
		return false
	}
	genDecl, ok := decl.(*dst.GenDecl)
	if !ok || genDecl.Tok != token.VAR {
		return false // Only check variable declarations
	}

	// Loop over the right-hand side expressions of each variable declaration
	for _, spec := range genDecl.Specs {
		if valueSpec, ok := spec.(*dst.ValueSpec); ok {
			for _, expr := range valueSpec.Values {
				found := ss.IdentifyScenarios(expr, ss.TestCase)
				if found {
					return true
				}
			}
		}
	}
	return false
}
