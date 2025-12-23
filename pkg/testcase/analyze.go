package testcase

import (
	"go/ast"
	"go/token"
	"go/types"
	"log/slog"
	"slices"

	"github.com/maxgreen01/go-test-analyzer/pkg/asttools"
)

// Attempts to extract the table-driven properties of a test case using information extracted from its parsed statements
func IdentifyScenarioSet(tc *TestCase, statements []*ExpandedStatement) *ScenarioSet {
	if tc == nil {
		slog.Error("Cannot identify Scenarios in nil TestCase")
		return nil
	}
	stmts := statements
	if len(stmts) == 0 {
		slog.Warn("Cannot identify ScenarioSet because there are no statements", "testCase", tc)
		return nil
	}

	// Initialize the TestCase's ScenarioSet, whose fields will be populated throughout this method with relevant data
	ss := &ScenarioSet{TestCase: tc}

	// Iterate test statements in reverse to find the runner loop before trying to find the scenarios
	stmtsReversed := slices.Clone(stmts)
	slices.Reverse(stmtsReversed)
outerStmtLoop:
	for _, expanded := range stmtsReversed {
		if expanded == nil {
			slog.Warn("Encountered nil statement in test case", "testCase", tc)
			continue outerStmtLoop
		}

		// Extract the loop that runs the subtests, which should not be part of a helper function (to reduce falsely identified table-driven tests)
		// todo NOTE - to allow subtest runners inside helper functions, move this block inside the loop over `expanded.All()`
		if ss.Runner == nil {
			stmt := expanded.Stmt
			// Detect the loop itself
			if rangeStmt, ok := stmt.(*ast.RangeStmt); ok {
				slog.Debug("Found range statement in test case", "testCase", tc.TestName)

				// Make sure the loop ranges over a valid data structure, and save it if so
				ss.detectScenarioDataStructure(tc.TypeOf(rangeStmt.X))

				if ss.DataStructure == ScenarioNoDS {
					// Can't do anything if the loop data structure is unknown
					slog.Debug("Detected a range loop in test case, but the data structure is unknown", "testCase", tc)
					continue outerStmtLoop // Try checking for additional loops
				}

				// Check if the scenario data structure is defined directly in the range statement
				if _, ok := rangeStmt.X.(*ast.CompositeLit); ok {
					scenariosDefinedInLoop := ss.IdentifyScenarios(rangeStmt.X, tc)
					if scenariosDefinedInLoop {
						slog.Debug("Found scenario definition directly in the range statement", "testCase", tc, "scenarios", len(ss.Scenarios))
					}
				}

				ss.Runner = rangeStmt

				continue outerStmtLoop // Move to the next statement
			}

			// todo LATER add support for `for-i` loops
			//  else if forStmt, ok := stmt.(*ast.ForStmt); ok {
			// 	slog.Debug("Found loop statement in test case", "test", t.Name)
			// 	t.TableDrivenType += ", with for loop"
			// 	detectTRun(forStmt.Body)
			// }
		}

		// Iterate over each component of the expanded statement, i.e. look into expanded helper functions
		for stmt := range expanded.All() {

			// Search for variable assignments matching the detected scenario data structure, with the goal of finding the scenario definitions
			if ss.Scenarios == nil && ss.ScenarioType != nil {
				switch assignment := stmt.(type) {
				case *ast.AssignStmt:
					// Statements like `scenarios := []Scenario{...}`
					for _, expr := range assignment.Rhs {
						found := ss.IdentifyScenarios(expr, tc)
						if found {
							slog.Debug("Found scenario definition in function body", "testCase", tc, "scenarios", len(ss.Scenarios))
							continue outerStmtLoop // Move to the next statement
						}
					}
				case *ast.DeclStmt:
					// Statements like `var scenarios = []Scenario{...}`
					// todo CLEANUP mostly the same code as checking in file decls below
					if genDecl, ok := assignment.Decl.(*ast.GenDecl); ok && genDecl.Tok == token.VAR {
						// Loop over the right-hand side expressions of each variable declaration
						for _, spec := range genDecl.Specs {
							if valueSpec, ok := spec.(*ast.ValueSpec); ok {
								for _, expr := range valueSpec.Values {
									found := ss.IdentifyScenarios(expr, tc)
									if found {
										slog.Debug("Found scenario definition in function body", "testCase", tc, "scenarios", len(ss.Scenarios))
										continue outerStmtLoop // Move to the next statement
									}
								}
							}
						}
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
		declLoop:
			for _, decl := range tc.GetFile().Decls {
				if genDecl, ok := decl.(*ast.GenDecl); ok {
					if genDecl.Tok != token.VAR {
						continue declLoop // Only check variable declarations
					}
					// Loop over the right-hand side expressions of each variable declaration
					for _, spec := range genDecl.Specs {
						if valueSpec, ok := spec.(*ast.ValueSpec); ok {
							for _, expr := range valueSpec.Values {
								found := ss.IdentifyScenarios(expr, tc)
								if found {
									slog.Debug("Found scenario definition in file declarations", "testCase", tc, "scenarios", len(ss.Scenarios))
									break declLoop // Stop checking file declarations
								}
							}
						}
					}
				}
			} // end of loop over file declarations
		}
	}

	// Attempt to perform additional analysis on the ScenarioSet
	ss.Analyze()
	return ss
}

// Detects the type of data structure used to store scenarios in a table-driven test as well as
// the underlying type (usually a struct) used to define scenarios, then saves both to the `ScenarioSet`.
// Also checks if the key of a map structure is used to define scenario names.
//
// Returns the ScenarioDataStructure value for this type and the underlying type used to define scenarios,
// which are both already saved to the `ScenarioSet`.
func (ss *ScenarioSet) detectScenarioDataStructure(typ types.Type) (ScenarioDataStructure, types.Type) {
	if typ == nil {
		ss.DataStructure, ss.ScenarioType = ScenarioNoDS, nil
		return ss.DataStructure, ss.ScenarioType
	}

	// Check the underlying type
	switch x := typ.Underlying().(type) {

	case *types.Slice:
		// Check for []struct
		if structType, ok := asttools.Unpointer(x.Elem()).Underlying().(*types.Struct); ok {
			ss.DataStructure, ss.ScenarioType = ScenarioStructListDS, structType
			return ss.DataStructure, ss.ScenarioType
		}
	case *types.Array:
		// Check for [N]struct
		if structType, ok := asttools.Unpointer(x.Elem()).Underlying().(*types.Struct); ok {
			ss.DataStructure, ss.ScenarioType = ScenarioStructListDS, structType
			return ss.DataStructure, ss.ScenarioType
		}

	case *types.Map:
		// Check for map[any]any
		// map[any]struct is expected most of the time, but something like map[string]bool is fine too
		ss.DataStructure = ScenarioMapDS
		ss.ScenarioType = asttools.Unpointer(x.Elem()).Underlying()

		// If the map key is a string (not considering underlying type), assume it's the scenario name
		if asttools.IsBasicType(x.Key(), types.IsString) {
			ss.NameField = "map key"
		}

		return ss.DataStructure, ss.ScenarioType
	}

	// Default or unknown case if other logic doesn't match
	ss.DataStructure, ss.ScenarioType = ScenarioNoDS, nil
	return ss.DataStructure, ss.ScenarioType
}

// Checks whether an expression has the same underlying type as the ScenarioType, and if so, saves the scenarios from the expression.
// Returns whether the scenarios were saved successfully. Always returns `false` if the `ScenarioSet.DataStructure` is unknown.
// See https://go.dev/ref/spec#Type_identity for details of the `types.Identical` comparison method.
func (ss *ScenarioSet) IdentifyScenarios(expr ast.Expr, tc *TestCase) bool {
	if tc == nil {
		slog.Error("Cannot identify Scenarios in nil TestCase")
		return false
	}

	// Both []struct and map are defined using a CompositeLit, so make sure this matches
	if compositeLit, ok := expr.(*ast.CompositeLit); ok {
		if len(compositeLit.Elts) == 0 {
			return false
		}

		// Depending on the scenario data structure, extract and save the scenarios themselves
		// todo LATER construct Scenario structs inside the cases.    also might have to make changes here to handle non-struct fields
		switch ss.DataStructure {

		case ScenarioStructListDS:
			// Scenarios are directly stored as the elements of the slice
			typ := tc.TypeOf(compositeLit.Elts[0])
			if typ != nil && types.Identical(typ.Underlying(), ss.ScenarioType) {
				ss.Scenarios = compositeLit.Elts
				return true
			}

		case ScenarioMapDS:
			// Scenarios are stored as the values of the `KeyValueExpr` elements
			kvExpr, ok := compositeLit.Elts[0].(*ast.KeyValueExpr)
			if ok && types.Identical(tc.TypeOf(kvExpr.Value).Underlying(), ss.ScenarioType) {
				for _, elt := range compositeLit.Elts {
					if kvExpr, ok := elt.(*ast.KeyValueExpr); ok {
						ss.Scenarios = append(ss.Scenarios, kvExpr)
					}
				}
				return true
			}
		}
	}
	return false
}
