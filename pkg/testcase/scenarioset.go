package testcase

import (
	"encoding/json"
	"go/types"
	"iter"
	"log/slog"
	"strings"

	"github.com/dave/dst"
	"github.com/maxgreen01/go-test-analyzer/pkg/asttools"
)

// Represents the properties of a table-driven test by storing information about the scenarios and their structure,
// as well as various analysis results derived from this information.
type ScenarioSet struct {
	// Reference to the TestCase this ScenarioSet belongs to
	TestCase *TestCase

	// Core data fields
	// todo LATER expand to support scenario definitions like `map[string]bool` without a struct template (probably by making changes to `DetectScenarioDataStructure`)
	ScenarioType types.Type // the data type that individual scenarios are based on, which may be an alias or pointer to another type containing the actual fields

	DataStructure ScenarioDataStructure // describes the type of data structure used to store scenarios
	Scenarios     []dst.Expr            // the individual scenarios themselves //todo LATER convert to type `[]Scenario`

	Runner             dst.Stmt   // the actual code that runs the scenarios (which is expected to be either a `ForStmt` or a `RangeStmt`)
	ScenarioStructName string     // the name of the variable representing the scenario data structure, which is iterated over by the Runner
	runnerIndexVar     *types.Var // the type of the variable representing the index (key) of the runner loop
	runnerValueVar     *types.Var // the type of the variable representing the scenario variable itself of the runner loop

	// Derived analysis results
	NameField         string   // the name of the field representing each scenario's name, or "map key" if the map key is used as the name
	ExpectedFields    []string // the names of fields representing the expected results of each scenario
	HasFunctionFields bool     // whether the scenario type has any fields whose type is a function
	UsesSubtest       bool     // whether the test calls `t.Run()` inside the loop body
}

//
// =============== Supporting Type Definitions ===============
//

// Represents the type of data structure used to store scenarios
type ScenarioDataStructure int

const (
	ScenarioNoDS ScenarioDataStructure = iota // no table-driven test structure detected

	// Primary data structures:
	// These are the most common data structures used to define table-driven tests, and are prioritized during detection.

	ScenarioStructListDS // table-driven test using a slice or array of structs
	ScenarioMapDS        // table-driven test using a map

	// Secondary data structures:
	// In contrast to primary data structures, these are fallback cases that are only detected during the second detection
	// pass in loops containing subtest definitions.

	ScenarioNonStructListDS // table-driven test using a slice or array of non-struct values (e.g. []string, []int)
	ScenarioOtherDS         // table-driven test using an unstructured runner (e.g. integer range loop)
)

// IsPrimary returns true if this is a primary data structure, as described above.
func (sds ScenarioDataStructure) IsPrimary() bool {
	switch sds {
	case ScenarioStructListDS, ScenarioMapDS:
		return true
	default:
		return false
	}
}

func (sds ScenarioDataStructure) String() string {
	switch sds {
	case ScenarioStructListDS:
		return "struct list"
	case ScenarioMapDS:
		return "map"
	case ScenarioNonStructListDS:
		return "non-struct list"
	case ScenarioOtherDS:
		return "other"
	default:
		return "none"
	}
}

func (sds ScenarioDataStructure) MarshalJSON() ([]byte, error) {
	return json.Marshal(sds.String())
}

func (sds *ScenarioDataStructure) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}
	switch str {
	case "struct list":
		*sds = ScenarioStructListDS
	case "map":
		*sds = ScenarioMapDS
	case "non-struct list":
		*sds = ScenarioNonStructListDS
	case "other":
		*sds = ScenarioOtherDS
	default:
		*sds = ScenarioNoDS
	}
	return nil
}

//
// =============== Analysis Methods ===============
//

// Perform additional analysis based on the core data fields, populating the corresponding fields
func (ss *ScenarioSet) Analyze() {
	ss.NameField = ss.detectNameField()
	ss.ExpectedFields = ss.detectExpectedFields()
	ss.HasFunctionFields = ss.NumFunctionFields() > 0
	ss.UsesSubtest, _ = ss.detectSubtest()

	// todo LATER consider expanding the statements inside the runner loop, just like with TestCase statements
	//     since TestCase already expands all statements, we can probably store a copy of the corresponding statement without recomputing
	//     This would also probably have to be looped into the refactoring code to replace DST data with a clone
}

// Returns the name of the field representing the name of each scenario
func (ss *ScenarioSet) detectNameField() string {
	// In the special case for map data structures where the key represents the scenario name,
	// the name field would already be set by `DetectScenarioDataStructure()`
	if ss.DataStructure == ScenarioMapDS && ss.NameField != "" {
		return ss.NameField
	}

	// If the scenario is defined in a different package, we can only use exported fields
	// todo LATER maybe there's a way to detect exported methods to access unexported fields?
	samePackage := ss.IsScenarioFromSamePackage()

	// If the scenario uses subtests, check if the first arg of `t.Run()` is a field of the scenario struct
	if ok, callExpr := ss.detectSubtest(); ok {
		// Get the first argument of the `t.Run()` call
		if len(callExpr.Args) > 0 {
			// TODO cleanup this check is very similar to `IsScenarioField()`, but this doesn't allow nested fields
			var argIdent *dst.Ident
			switch expr := callExpr.Args[0].(type) {
			case *dst.SelectorExpr:
				argIdent = expr.Sel
			case *dst.Ident:
				argIdent = expr
			}

			// Check if the variable matches one of the scenario fields
			if argVar, ok := ss.TestCase.ObjectOf(argIdent).(*types.Var); ok {
				for field := range ss.GetFields() {
					if field == argVar {
						// Found a matching field!
						// Note that the special "map key" case is already handled above, so we shouldn't need to handle it here
						return field.Name()
					}
				}
			}
		}
		// If the test uses `t.Run()` but the first arg doesn't match one of the fields, consider this to not have a name field
		return ""
	}

	// Fallback: match field names by substring search (ensuring the field is a string)
	for field := range ss.GetFields() {
		if !asttools.IsBasicType(field.Type(), types.IsString) {
			// Skip non-string fields
			continue
		}
		if !samePackage && !field.Exported() {
			// Skip unexported fields if the scenario is in a different package
			continue
		}
		lowercase := strings.ToLower(field.Name())
		if strings.Contains(lowercase, "name") || strings.Contains(lowercase, "desc") {
			return field.Name()
		}
	}
	return ""
}

// Returns the names of the fields representing the expected results of each scenario
// todo LATER try expanding this to detect fields that are used in assertions or comparisons
func (ss *ScenarioSet) detectExpectedFields() []string {
	// If the scenario is defined in a different package, we can only use exported fields
	samePackage := ss.IsScenarioFromSamePackage()

	// Save the names of fields containing the string "expect", "want", or "result"
	var expectedFields []string
	for field := range ss.GetFields() {
		if !samePackage && !field.Exported() {
			// Skip unexported fields if the scenario is in a different package
			continue
		}
		lowercase := strings.ToLower(field.Name())
		if strings.Contains(lowercase, "expect") || strings.Contains(lowercase, "want") || strings.Contains(lowercase, "result") {
			expectedFields = append(expectedFields, field.Name())
		}
	}
	return expectedFields
}

// Returns the number of fields in the scenario type whose type is a function
func (ss *ScenarioSet) NumFunctionFields() int {
	count := 0
	for field := range ss.GetFields() {
		if _, ok := asttools.UnderlyingType(field.Type()).(*types.Signature); ok {
			count++
		}
	}
	return count
}

// Returns a bool indicating whether `t.Run()` is called inside the loop body (possibly nested), as well as a reference to the `t.Run()` statement
func (ss *ScenarioSet) detectSubtest() (bool, *dst.CallExpr) {
	tc := ss.TestCase
	// Detect the name of the `testing.T` parameter instead of hardcoding "t"
	tVarName, err := GetTestingParamName(tc.funcDecl)
	if err != nil {
		slog.Warn("Cannot detect `*testing.T` parameter in test case", "err", err, "test", tc)
		return false, nil
	}

	statements := ss.GetRunnerStatements()
	var detected *dst.CallExpr
	for _, stmt := range statements {
		dst.Inspect(stmt, func(n dst.Node) bool {
			if stmt == nil || detected != nil {
				return false
			}
			if callExpr, ok := n.(*dst.CallExpr); ok {
				if asttools.MatchSelectorExpr(callExpr.Fun, tVarName, "Run") {
					detected = callExpr
					return false
				}
			}
			return true
		})
		if detected != nil {
			return true, detected
		}
	}
	return false, nil
}

// todo add more analysis methods, like whether the scenario type and/or scenarios themselves are defined outside the function by comparing their `Pos` against the overall test function's bounds

//
// =============== Result Getters ===============
//

// Returns an iterator over the "fields" of the data type used to define scenarios, including more than just struct fields.
// If the scenario data structure is a map, the iterator includes the variable representing the map key.
// If the scenario type is a struct, the iterator includes the struct's fields.
// If the scenario type is not a struct, the iterator includes the variable representing the scenario variable itself.
//
// Note that this yields distinct elements for each individual struct field because it uses the Type system, even though
// fields defined like `a, b int` are treated by the AST as one `ast.Field` with multiple `Names`.
func (ss *ScenarioSet) GetFields() iter.Seq[*types.Var] {
	// Manually construct the iterator so we have full control of its elements.
	// Note that we can never return a `nil` iterator, because that would cause a panic if the caller tries to range over it.
	return func(yield func(*types.Var) bool) {
		// If scenarios are defined as a map, consider the map key (accessed by the runner loop key) as an additional "field"
		if ss.DataStructure == ScenarioMapDS && ss.runnerIndexVar != nil {
			if !yield(ss.runnerIndexVar) {
				return
			}
		}

		// If the scenario type is a struct, push all of its fields
		if structType, ok := asttools.UnderlyingType(ss.ScenarioType).(*types.Struct); ok {
			// Equivalent to (but more direct than) ranging over the `Fields()` iterator and yielding each field individually
			structType.Fields()(yield)

		} else if ss.runnerValueVar != nil {
			// Otherwise, consider the entire scenario (accessed by the runner loop value) as a single "field"
			if !yield(ss.runnerValueVar) {
				return
			}
		}
	}
}

// Returns true if the provided expression represents the scenario object itself or one of its fields,
// with support for nested fields and index-based accesses.
func (ss *ScenarioSet) IsScenarioField(expr dst.Expr) bool {
	if ss == nil || expr == nil || ss.TestCase == nil {
		return false
	}
	tc := ss.TestCase

	// Get the root identifier and the innermost selector and index expressions (if any are present)
	var innermostSelector *dst.SelectorExpr
	var innermostIndexExpr *dst.IndexExpr
	rootIdent := asttools.GetRootIdent(expr, func(e dst.Expr) {
		if selectorExpr, ok := e.(*dst.SelectorExpr); ok {
			innermostSelector = selectorExpr
		} else if indexExpr, ok := e.(*dst.IndexExpr); ok {
			innermostIndexExpr = indexExpr
		}
	})

	// First, make sure the base expression is a reference to the scenario variable itself
	isScenarioBaseExpr := false

	// Check if the base expression is a direct reference to the scenario variable itself, i.e. if the root of the expression resolves to the same variable as the value of the runner loop.
	if obj := tc.ObjectOf(rootIdent); obj != nil {
		if ss.runnerValueVar != nil && obj == ss.runnerValueVar {
			isScenarioBaseExpr = true
		}
	}
	// Note: this more lenient check allows scenario variable detection based on type only, which is good for potentially detecting scenario fields in expressions
	// involving proxy variables (e.g. `tc := tests[i]`), but may also produce false positives in expressions involving variables of the same type as the scenario
	// that are used for other purposes. An egregious example of this is when the scenario type is `bool` (e.g. when scenarios are map[string]bool), meaning any
	// regular boolean variable would be considered a scenario field.
	/* if typ := tc.TypeOf(rootIdent); typ != nil && types.Identical(asttools.Unpointer(typ), asttools.Unpointer(ss.ScenarioType)) {
		return true
	} */

	// Check if the base expression is an index-based reference to the scenario variable itself, with the form `scenarioStructName[runnerIndexVar]`.
	// We don't use `rootIdent` here because because it's safer to make sure the container is an ident, in case the innermost index-expression isn't the root of the expression.
	if innermostIndexExpr != nil {
		if container, ok := innermostIndexExpr.X.(*dst.Ident); ok && container.Name == ss.ScenarioStructName {
			if indexIdent, ok := innermostIndexExpr.Index.(*dst.Ident); ok {
				if indexObj := tc.ObjectOf(indexIdent); ss.runnerIndexVar != nil && indexObj == ss.runnerIndexVar {
					isScenarioBaseExpr = true
				}
			}
		}
	}

	// Check if the target identifier resolves to a field/variable yielded by `GetFields()`.
	// Use the target of the selection (i.e. possibly a direct field of the scenario) if possible, otherwise use the base identifier itself (i.e. possibly the runner loop key for a map).
	// This allows us to handle references like `tc.field`, `tc.field.subfield`, `tests[i].field`, `(*tests[i].field[0]).subfield`, etc. by always using `field` as the target identifier.
	var targetIdent *dst.Ident
	if innermostSelector != nil {
		targetIdent = innermostSelector.Sel
	} else if rootIdent != nil {
		targetIdent = rootIdent
	}
	if targetVar, ok := tc.ObjectOf(targetIdent).(*types.Var); ok {
		for field := range ss.GetFields() {
			if field != targetVar {
				continue
			}
			// Found the matching scenario field/variable!
			// If the variable is a struct field (i.e. the expression involves a `SelectorExpr`), then the base expression must be the scenario variable itself.
			// This avoids false positives when accessing fields of structs with the same type as the scenario, even though the object isn't actually the scenario itself.
			// If the variable is *not* a struct field (i.e. the expression does not involve a `SelectorExpr`), then the scenario variable isn't actually needed.

			// Sanity check of the above assumption that the matching field is a struct field if and only if the expression involves a `SelectorExpr`
			if field.IsField() != (innermostSelector != nil) {
				slog.Warn("Inconsistent combination of scenario field type and selector expression", "testcase", tc, "isField", field.IsField(), "hasSelectorExpr", innermostSelector != nil)
				return false
			}

			// This condition represents the logic in the comment above, and is more easily understood in this longer form than the equivalent `!field.IsField() || isScenarioBaseExpr`.
			return (field.IsField() && isScenarioBaseExpr) || !field.IsField()
		}
	}

	// If the expression didn't match any of the fields, it could only be part of the scenario if it's the scenario variable itself
	return isScenarioBaseExpr
}

// Returns the statements that make up the loop body
func (ss *ScenarioSet) GetRunnerStatements() []dst.Stmt {
	if ss.Runner == nil {
		return nil
	}

	var body *dst.BlockStmt
	switch loop := ss.Runner.(type) {
	case *dst.RangeStmt:
		body = loop.Body
	case *dst.ForStmt:
		body = loop.Body
	}
	if body == nil {
		return nil
	}

	return body.List
}

// Returns whether the detected information in the ScenarioSet is indicative of a table-driven test
func (ss *ScenarioSet) IsTableDriven() bool {
	if ss == nil {
		return false
	}

	// Heuristic: looped subtests are always indicative of table-driven tests, even without a valid scenario type or defined scenarios
	if ss.Runner != nil && ss.UsesSubtest {
		return true
	}

	// Heuristic: if a map doesn't have a string key or an explicit "name" field, it's probably not a table-driven test
	// TODO note - this is probably not an accurate statement
	// if !asttools.IsBasicType(x.Key(), types.IsString) && ss.NameField == "" {
	// 	ss.DataStructure, ss.ScenarioType = ScenarioNoDS, nil
	// }
	return ss.DataStructure != ScenarioNoDS && ss.ScenarioType != nil && len(ss.Scenarios) > 0
}

// Returns whether the scenario type is defined in the same package as the test function
func (ss *ScenarioSet) IsScenarioFromSamePackage() bool {
	if ss == nil || ss.TestCase == nil || ss.ScenarioType == nil {
		return false
	}

	// If scenarios are defined with a named type, we can check its underlying types.Object (i.e. its definition) directly.
	// We use `types.Unalias` to find the underlying type without losing the types.Named data.
	if unaliased := types.Unalias(asttools.Unpointer(ss.ScenarioType)); unaliased != nil {
		if namedType, ok := unaliased.(*types.Named); ok {
			pkg := namedType.Obj().Pkg()
			return pkg != nil && pkg.Path() == ss.TestCase.ImportPath
		}
	}
	// If the type isn't named then it must be anonymous, meaning it's definitely from the same package
	return true
}

//
// =============== Output Methods ===============
//

// Helper struct for Marshaling and Unmarshaling JSON.
// Transforms all DST nodes to their string representations.
type scenarioSetJSON struct {
	// Parent TestCase is deliberately not included

	ScenarioType string `json:"scenarioType"` // NOTE: this should be the underlying type, not the pointer or alias

	DataStructure ScenarioDataStructure `json:"dataStructure"`
	Scenarios     []string              `json:"scenarios"`

	Runner             string `json:"runner"`
	ScenarioStructName string `json:"scenarioStructName"`

	NameField         string   `json:"nameField"`
	ExpectedFields    []string `json:"expectedFields"`
	HasFunctionFields bool     `json:"hasFunctionFields"`
	UsesSubtest       bool     `json:"usesSubtest"`
	IsTableDriven     bool     `json:"isTableDriven"` // isn't an actual field on the original struct
}

// Marshal the ScenarioSet for JSON output
func (ss *ScenarioSet) MarshalJSON() ([]byte, error) {
	if ss == nil || ss.TestCase == nil {
		// Can't do anything with improperly initialized ScenarioSet, so return empty JSON data
		return json.Marshal(scenarioSetJSON{})
	}

	var scenarioTypeStr string
	if ss.ScenarioType != nil {
		scenarioTypeStr = asttools.UnderlyingType(ss.ScenarioType).String()
	}

	// Marshal individual Scenario data
	// todo LATER remove when implement Marshal in Scenario
	scenarioStrs := make([]string, len(ss.Scenarios))
	for i, node := range ss.Scenarios {
		scenarioStrs[i] = asttools.NodeToString(node)
	}

	return json.Marshal(scenarioSetJSON{
		ScenarioType: scenarioTypeStr,

		DataStructure: ss.DataStructure,
		Scenarios:     scenarioStrs,

		Runner:             asttools.NodeToString(ss.Runner),
		ScenarioStructName: ss.ScenarioStructName,

		NameField:         ss.NameField,
		ExpectedFields:    ss.ExpectedFields,
		HasFunctionFields: ss.HasFunctionFields,
		UsesSubtest:       ss.UsesSubtest,
		IsTableDriven:     ss.IsTableDriven(),
	})
}

// todo CLEANUP add UnmarshalJSON method
