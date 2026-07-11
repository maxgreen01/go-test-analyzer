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
			var body *dst.BlockStmt
			switch loop := stmt.(type) {
			case *dst.RangeStmt:
				body = loop.Body
			case *dst.ForStmt:
				body = loop.Body
			}
			if body == nil {
				continue outerStmtLoop // not a loop, or empty body
			}

			// Check if the loop involves a scenario data structure
			ds := ss.detectLoopScenarioDS(stmt)
			if ds == nil {
				slog.Debug("Detected a loop in test case, but didn't find a valid scenario structure", "testCase", tc)
				continue outerStmtLoop
			}

			// Do supplementary checks before saving the data structure results

			// Heuristic: the scenario data structure itself should never be mutated inside the loop body, e.g. `scenarios = append(scenarios, ...)`
			if ds.structName != "" && isAssigned(dst.NewIdent(ds.structName), body.List) {
				slog.Debug("Scenario data structure is mutated in loop body, so not table-driven", "testCase", tc, "variableName", ds.structName)
				continue outerStmtLoop
			}

			// All checks passed, so save the detected values to the ScenarioSet
			ss.Runner = stmt
			ss.DataStructure = ds.dataStructure
			ss.ScenarioType = ds.scenarioType
			ss.ScenarioStructName = ds.structName
			ss.NameField = ds.nameField

			// Before moving to other statements, check if the scenarios are defined directly in the range statement we just found
			if rangeStmt, ok := stmt.(*dst.RangeStmt); ok {
				if _, ok := rangeStmt.X.(*dst.CompositeLit); ok {
					if ss.IdentifyScenarios(rangeStmt.X, tc) {
						slog.Debug("Found scenario definition directly in the range statement", "testCase", tc, "scenarios", len(ss.Scenarios))
					}
				}
			}

			continue outerStmtLoop // Move to the next test statement
		} // end of check for Runner loop

		// Iterate over each component of the expanded statement, i.e. look into expanded helper functions
		for stmt := range expanded.AllStatements() {

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

// detectedDS is an intermediate representation of a detected scenario data structure found by one of the `detect...` methods.
// The data stored in these structs could be transferred to the ScenarioSet fields if additional criteria are met.
type detectedDS struct {
	dataStructure ScenarioDataStructure
	scenarioType  types.Type
	structName    string
	nameField     string
}

// Checks if the provided expression represents a data structure used to store scenarios in a
// table-driven test based on the underlying data type (usually a struct) used to define scenarios.
// Also checks if the key of a map structure is used to define each scenario's name.
func (ss *ScenarioSet) detectScenarioDataStructure(expr dst.Expr) *detectedDS {
	typ := ss.TestCase.TypeOf(expr)
	if typ == nil {
		return nil
	}
	
	var ds *detectedDS
	// Check the underlying type of the whole data structure
	switch x := typ.Underlying().(type) {

	case *types.Slice, *types.Array:
		// Check for []struct or [N]struct
		elemType := x.(asttools.Elemer).Elem()
		if _, ok := asttools.UnderlyingType(elemType).(*types.Struct); ok {
			ds = &detectedDS{dataStructure: ScenarioStructListDS, scenarioType: elemType} // save the original data type, not the underlying one
		}

	case *types.Map:
		// Check for map[any]any
		// map[any]struct is expected most of the time, but something like map[string]bool is fine too
		ds = &detectedDS{dataStructure: ScenarioMapDS, scenarioType: x.Elem()}

		// If the map key is a string (not considering underlying type), assume it's the scenario name
		if asttools.IsBasicType(x.Key(), types.IsString) {
			ds.nameField = "map key"
		}
	default:
		// Not a recognized data structure type
		return nil
	}

	// If this is a recognized scenario data structure, the provided expression represents the scenarios themselves.
	// This name could be empty if scenarios are defined directly in a range statement (e.g. as a CompositeLit or CallExpr) without being
	// assigned to a separate variable, but this means the range statement should already have a value expression to reference the current scenario.
	if ident, ok := expr.(*dst.Ident); ds != nil && ok {
		ds.structName = ident.Name
	}
	return ds
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

// detectLoopScenarioDS extracts the scenario data structure descriptor from a loop statement (either RangeStmt or ForStmt).
// Returns nil if no valid scenario data structure is found.
func (ss *ScenarioSet) detectLoopScenarioDS(stmt dst.Stmt) *detectedDS {
	var indexVarName string
	var possibleLenExprs []dst.Expr
	var body *dst.BlockStmt

	switch loop := stmt.(type) {

	case *dst.RangeStmt:
		// Easy case: check for range directly over a valid scenario data structure, e.g. `for _, s := range scenarios`
		if ds := ss.detectScenarioDataStructure(loop.X); ds != nil {
			return ds
		}

		// Try using the loop index (key) variable to find a scenario data structure in the loop body, e.g. in `for i := range len(scenarios)`.
		// The index variable can technically be defined outside the loop, but that case is intentionally ignored to limit false detections.
		if loop.Key != nil {
			if keyIdent, ok := loop.Key.(*dst.Ident); ok {
				indexVarName = keyIdent.Name
			}
		}
		possibleLenExprs = []dst.Expr{loop.X}
		body = loop.Body

	case *dst.ForStmt:
		// Try using the inferred loop index variable to find a scenario data structure in the loop body, e.g. in `for i := 0; i < len(scenarios); i++`.
		if indexIdent := GetForStmtIndexIdent(loop); indexIdent != nil {
			indexVarName = indexIdent.Name
		}
		if cond, ok := loop.Cond.(*dst.BinaryExpr); ok {
			possibleLenExprs = []dst.Expr{cond.Y, cond.X}
		}
		body = loop.Body
	}

	if indexVarName == "" || body == nil {
		return nil
	}

	// First, find all valid scenario data structures that are indexed using the key/index variable.
	// If this loop is a range over the len() of a valid data structure or has a len() check in the
	// condition, prioritize that variable. Otherwise, fallback to the first detected structure.
	if indexedStructures := ss.detectScenariosByIndex(body, indexVarName); len(indexedStructures) > 0 {
		// Prioritize the argument to len()
		for _, expr := range possibleLenExprs {
			if lenArg := getLenArgName(expr); lenArg != "" {
				for _, ds := range indexedStructures {
					if ds.structName == lenArg {
						return ds
					}
				}
			}
		}
		// Fallback to the first detected structure
		return indexedStructures[0]
	}

	// No valid scenario data structure found, even after checking indexing
	return nil
}

// Checks if the expression is a call to `len(x)`, and returns the name of the argument `x` if it is an identifier.
func getLenArgName(expr dst.Expr) string {
	if callExpr, ok := expr.(*dst.CallExpr); ok {
		if ident, ok := callExpr.Fun.(*dst.Ident); ok && ident.Name == "len" && len(callExpr.Args) > 0 {
			if argIdent, ok := callExpr.Args[0].(*dst.Ident); ok {
				return argIdent.Name
			}
		}
	}
	return ""
}

// Searches through a block statement to find all index statements on valid scenario data structures
// based on `detectScenarioDataStructure()` using the given index variable name. The index expression
// must never appear on the LHS of an assignment, since scenario data should be read-only.
// Returns a slice of detected structures in the order that they appear based on a DST traversal.
func (ss *ScenarioSet) detectScenariosByIndex(body *dst.BlockStmt, indexVarName string) []*detectedDS {
	if indexVarName == "" || indexVarName == "_" || body == nil {
		return nil
	}
	
	// Keep track of all the valid scenario data structures that have been found already
	var found []*detectedDS
	seen := make(map[string]bool)

	dst.Inspect(body, func(n dst.Node) bool {
		// Index expression like `container[indexVarName]`
		if indexExpr, ok := n.(*dst.IndexExpr); ok {
			if indexIdent, ok := indexExpr.Index.(*dst.Ident); ok && indexIdent.Name == indexVarName {
				if containerIdent, ok := indexExpr.X.(*dst.Ident); ok {
					if seen[containerIdent.Name] {
						return true
					}
					// Ignore this expression if the variable is ever on the LHS of assignment in the body (must be read-only)
					if isAssigned(containerIdent, body.List) {
						return true
					}
					// Try to detect scenario data structure of the container
					if ds := ss.detectScenarioDataStructure(containerIdent); ds != nil {
						seen[containerIdent.Name] = true
						found = append(found, ds)
					}
				}
			}
		}
		return true
	})
	return found
}

// Searches through a declaration for variable assignments matching the detected scenario data structure
// by checking the `ScenarioStructName` and using `IdentifyScenarios()`. Returns true and saves the
// scenarios to the `ScenarioSet` if a matching assignment is found.
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
			for i, expr := range valueSpec.Values {
				nameIdent := valueSpec.Names[i]
				if ss.ScenarioStructName == "" || nameIdent == nil || nameIdent.Name != ss.ScenarioStructName {
					// Skip if the variable name doesn't match the detected data structure name
					continue
				}
				found := ss.IdentifyScenarios(expr, ss.TestCase)
				if found {
					return true
				}
			}
		}
	}
	return false
}
