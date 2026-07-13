package testcase

import (
	"encoding/json"
	"go/types"
	"log/slog"
	"strings"

	"github.com/dave/dst"
	"github.com/maxgreen01/go-test-analyzer/pkg/asttools"
)

// Represents the result of analyzing the loops found in a test case.
type LoopAnalysisResult struct {
	// The number of detected local loops, not including nested loops
	NumLocalLoops int `json:"numLocalLoops"`

	// The number of detected delegated loops, not including nested loops
	NumDelegatedLoops int `json:"numDelegatedLoops"`

	// Detected loops found in the test case, with nested loops stored internally
	Loops []*Loop `json:"loops"`
}

// Detects and analyzes all the loops found in the expanded statements of a test case, including those called in expanded function calls
func AnalyzeLoops(tc *TestCase, parsedStmts []*ExpandedStatement) *LoopAnalysisResult {
	loopAnalysis := &LoopAnalysisResult{}
	topLevelSeen := make(map[dst.Node]bool)

	// Analyze each top-level statement's ExpandedStatement tree
	for _, expanded := range parsedStmts {
		loops := analyzeStmt(expanded, nil, tc, topLevelSeen)
		loopAnalysis.Loops = append(loopAnalysis.Loops, loops...)
	}

	for _, l := range loopAnalysis.Loops {
		if l.Delegated {
			loopAnalysis.NumDelegatedLoops++
		} else {
			loopAnalysis.NumLocalLoops++
		}
	}
	return loopAnalysis
}

// Returns the number of detected non-nested loops that are indicative of a table-driven test
func (loopAnalysis *LoopAnalysisResult) CountTableDriven() int {
	count := 0
	for _, loop := range loopAnalysis.Loops {
		if loop.IndicatesTableDriven {
			count++
		}
	}
	return count
}

// Recursively inspects a statement's DST structure (including expanded function calls) and type information to detect loops,
// including nested loops and metadata features. Uses pre-built ExpandedStatements to expand function calls and avoid cycles.
// Builds a hierarchy of loops by tracking a reference to the closest parent loop and modifying its fields in-place.
// Avoids saving duplicate loops within each loop's nested slice, but allows repeats among different parent loops.
// Returns a slice of all detected top-level (non-nested) loops if no parent is provided, or nil if a parent is provided.
func analyzeStmt(expanded *ExpandedStatement, parentLoop *Loop, tc *TestCase, seen map[dst.Node]bool) []*Loop {
	if expanded == nil || expanded.Stmt == nil {
		return nil
	}
	var topLevelLoops []*Loop // only used if `parentLoop` is nil

	dst.Inspect(expanded.Stmt, func(node dst.Node) bool {
		if node == nil {
			return false
		}
		switch n := node.(type) {
		case *dst.RangeStmt, *dst.ForStmt:
			// Ignore loops that have already been analyzed directly under this parent
			if seen[n] {
				return false
			}

			// Analyze the loop and detect any children using recursion
			loop := CreateLoop(n, expanded.Children, tc)

			// Save nested loops under their parent, or collect top-level loops to return
			if parentLoop != nil {
				parentLoop.NestedLoops = append(parentLoop.NestedLoops, loop)
			} else {
				topLevelLoops = append(topLevelLoops, loop)
			}
			seen[n] = true

			// Stop inspecting descendants because the body has already been processed by `CreateLoop`
			return false

		case *dst.FuncLit:
			// Only descend into the function literal if it is the subject of the current analysis
			// (e.g., when analyzing a function literal argument to `t.Run`). Other function literals
			// will be analyzed separately when they are called.
			if exprStmt, ok := expanded.Stmt.(*dst.ExprStmt); ok && exprStmt.X == n {
				return true
			}
			return false

		case *dst.CallExpr:
			// Check for new metadata features if this is happening inside a loop
			if parentLoop != nil {
				phase := categorizeCallExpr(n, tc)
				isDelegated := parentLoop.Delegated || !tc.IsWithinTestFunction(n)

				parentLoop.HasSubtest.register(phase == TestPhaseSubtest, isDelegated)
				parentLoop.HasAssertion.register(phase == TestPhaseAssert, isDelegated)
			}

			// Expand the call's children, as found in the pre-computed ExpandedStatement tree of the most recently expanded
			// function call or original statement. This is "equivalent" to expanding the statement anew, but avoids cycles
			// and saves redundant processing. We must search the entire tree of children in case the `CallExpr` is nested
			// inside another statement (e.g. an assignment) and isn't an immediate child of the original statement. This
			// also allows us to match against the original statement itself.
			// Note: from the ExpandedStatement internals, we expect that a `CallExpr` must be wrapped in `ExprStmt`.
			var targetChildren []*ExpandedStatement
			for estmt := range expanded.All() {
				if exprStmt, ok := estmt.Stmt.(*dst.ExprStmt); ok && exprStmt.X == n {
					targetChildren = estmt.Children
					break
				}
			}

			// Analyze the statements inside the expanded function call's children
			for _, child := range targetChildren {
				childLoops := analyzeStmt(child, parentLoop, tc, seen)
				if parentLoop == nil {
					// If any new top-level loops were found, save them to be returned
					topLevelLoops = append(topLevelLoops, childLoops...)
				}
			}

			// Stop inspecting descendants because we already manually analyzed all the call's children
			return false
		}
		// Continue descending to search for loops or function calls
		return true
	})
	return topLevelLoops
}

// TODO maybe add a way to save each Loop as a distinct CSV row, but this would be annoying to implement given the inherent split
//   with the AnalysisResult (which contains all the identifying/location information) and the optional nature of the loop analysis in the first place

// ================================================================================================

// Represents metadata about a loop statement detected as part of a test case.
type Loop struct {
	// The classification of the loop structure
	LoopType LoopType `json:"loopType"`

	// The DST code of the loop itself, converted to a string
	Content string `json:"content"`

	// Whether the loop is indicative of a table-driven test
	IndicatesTableDriven bool `json:"indicatesTableDriven"`

	// Whether the loop defines subtests using the built-in `t.Run` method or a library-based equivalent
	HasSubtest loopFeature `json:"hasSubtest"`

	// Whether the loop contains an assertion, detected based on the presence of built-in or library-based test failure functions
	HasAssertion loopFeature `json:"hasAssertion"`

	// Whether the loop directly mutates any data defined outside the loop
	DoesExternalMutation bool `json:"doesExternalMutation"`

	// Whether the loop itself or any of its relevant analysis statements are found outside the function body
	Delegated bool `json:"delegated"`

	// Additional loops that are contained within this loop, if any
	NestedLoops []*Loop `json:"nestedLoops,omitempty"`
}

// Creates a Loop instance based on the provided DST node, pre-computed ExpandedStatement children, and the TestCase that uses it.
// Note that `children` is expected to be a superset of the statements actually inside the loop body, since it should hold all the child
// statements of the most recently expanded function call or original statement, and may include statements next to the loop.
// Analyzes the loop's inner statements to detect nested loops and metadata features before finalizing, i.e. in a depth-first traversal.
func CreateLoop(stmt dst.Node, children []*ExpandedStatement, tc *TestCase) *Loop {
	loop := &Loop{
		Content:   asttools.NodeToString(stmt),
		Delegated: !tc.IsWithinTestFunction(stmt),
	}

	// Inspect the structure of the loop itself
	var body *dst.BlockStmt
	switch loopStmt := stmt.(type) {
	case *dst.RangeStmt:
		loop.LoopType = classifyRangeLoop(loopStmt, tc)
		body = loopStmt.Body
	case *dst.ForStmt:
		loop.LoopType = classifyForLoop(loopStmt, tc)
		body = loopStmt.Body
	}

	// Analyze nested statements using a dummy ExpandedStatement representing the loop body, and reuse the containing statement's children.
	// Use a fresh `seen` map to avoid duplicate loops within this new parent, but allow them to be repeated under different parents.
	// Any detected nested loops or metadata features are automatically attached to this loop.
	analyzeStmt(&ExpandedStatement{Stmt: body, Children: children}, loop, tc, make(map[dst.Node]bool))
	loop.checkExternalMutation(stmt, tc)

	// Make sure all metadata features are set correctly
	loop.finalize()

	return loop
}

// Classifies a range statements into one of the LoopTypeRange[...] types based on the type of the data being ranged over.
func classifyRangeLoop(stmt *dst.RangeStmt, tc *TestCase) LoopType {
	if tc == nil {
		return LoopTypeRangeOther
	}
	typ := tc.TypeOf(stmt.X)
	if typ == nil {
		return LoopTypeRangeOther
	}

	// Determine the type of the data being ranged over
	switch x := typ.Underlying().(type) {
	case *types.Slice, *types.Array:
		if _, ok := asttools.UnderlyingType(x.(asttools.Elemer).Elem()).(*types.Struct); ok {
			return LoopTypeRangeStructs
		}
		return LoopTypeRangeNonStructs
	case *types.Map:
		return LoopTypeRangeMap
	case *types.Basic:
		if asttools.IsBasicType(typ, types.IsInteger) {
			return LoopTypeRangeInt
		}
	}

	return LoopTypeRangeOther
}

// Classifies a non-range loop statement based on the structure of its clauses.
func classifyForLoop(stmt *dst.ForStmt, tc *TestCase) LoopType {
	if stmt.Init == nil && stmt.Cond == nil && stmt.Post == nil {
		return LoopTypeInfinite
	}
	if stmt.Init == nil && stmt.Cond != nil && stmt.Post == nil {
		return LoopTypeConditionOnly
	}

	// The loop has Init and/or Post statements, so it's iterative
	indexIdent := GetForStmtIndexIdent(stmt) // todo CLEANUP maybe don't require that this is an ident, which would allow using a struct field (for example) as the index variable
	if indexIdent != nil && tc != nil {
		typ := tc.TypeOf(indexIdent)
		if typ != nil && asttools.IsBasicType(typ, types.IsInteger) {
			return LoopTypeIterativeIndexed
		}
	}

	return LoopTypeIterativeNonIndexed
}

// Represents a metadata feature of a loop, acting like a regular boolean with an additional flag indicating whether the feature could only be detected outside the original test function.
// todo CLEANUP - maybe only Marshal the `Present` field (and make fields private) to hide the fact that this isn't just a boolean. This is simpler, but it obscures the source of the delegated flag.
type loopFeature struct {
	// Whether the feature is present in the loop or any of its children
	Present bool `json:"present"`
	// Whether the feature can only be detected outside the original test function
	Delegated bool `json:"delegated"`
}

// Sets the metadata feature flag to `true` if the feature is present. Also inherits the delegated flag if the feature is first being registered,
// or is set back to `false` if a local version is found later. If it's been found locally, it won't ever be set back to delegated.
func (lf *loopFeature) register(present bool, isDelegated bool) {
	if present {
		// The value of `isDelegated` only gets stored when the feature is first registered, or if the feature was already delegated.
		// If the feature is already delegated, it will stay delegated until a local version is found.
		lf.Delegated = (!lf.Present || lf.Delegated) && isDelegated
		lf.Present = true
	}
}

// Bubbles up detected metadata features from nested loops, and populates computed fields based on other analysis results
func (loop *Loop) finalize() {
	for _, child := range loop.NestedLoops {
		// Bubble up any `true` attributes from nested loops, inheriting the delegated status of the entire child itself or the individual feature.
		// If the feature gets set to delegated, it indicates that the feature could not be found locally inside the original test function.
		loop.HasSubtest.register(child.HasSubtest.Present, child.Delegated || child.HasSubtest.Delegated)
		loop.HasAssertion.register(child.HasAssertion.Present, child.Delegated || child.HasAssertion.Delegated)
	}

	// If this loop isn't already delegated, it becomes delegated if any of its features are
	if !loop.Delegated {
		isSubtestDelegated := loop.HasSubtest.Present && loop.HasSubtest.Delegated
		isAssertionDelegated := loop.HasAssertion.Present && loop.HasAssertion.Delegated

		if isSubtestDelegated || isAssertionDelegated {
			loop.Delegated = true
		}
	}

	// Classify this loop based on its fully bubbled properties
	if loop.HasSubtest.Present || (loop.HasAssertion.Present && !loop.DoesExternalMutation) {
		loop.IndicatesTableDriven = true
	}
}

// Inspects the loop body to check for any mutations of variables defined outside the loop's scope.
func (loop *Loop) checkExternalMutation(loopStmt dst.Node, tc *TestCase) {
	if loop == nil || loopStmt == nil || tc == nil {
		return
	}

	loopScope := tc.GetNodeScope(loopStmt)

	var body *dst.BlockStmt
	switch s := loopStmt.(type) {
	case *dst.RangeStmt:
		body = s.Body
	case *dst.ForStmt:
		body = s.Body
	}

	if body == nil {
		return
	}

	// Check for mutations via assignments and increment/decrement statements on variables outside the loop scope
	if !loop.DoesExternalMutation {
		for _, lhs := range asttools.FindModifiedExpressions(body) {
			if isExternalMutation(lhs, tc, loopScope) {
				loop.DoesExternalMutation = true
				break
			}
		}
	}
}

// TestPhase is used to categorize test statements into the test phases.
// TODO CLEANUP this is very basic & temporary/underused for now
type TestPhase int

const (
	TestPhaseUnknown TestPhase = iota
	TestPhaseArrange           // setup or initialization code
	TestPhaseSubtest           // subtest execution via `t.Run()`
	TestPhaseAct               // production code under test
	TestPhaseAssert            // assertion or test failure functions
)

// Determine which test phase a call expression falls into.
// TODO make this more robust and return fewer "unknown"
func categorizeCallExpr(callExpr *dst.CallExpr, tc *TestCase) TestPhase {
	if tc == nil {
		return TestPhaseUnknown
	}

	// Get the identifier holding the name of the function being called
	var ident *dst.Ident
	switch x := callExpr.Fun.(type) {
	case *dst.Ident:
		ident = x
	case *dst.SelectorExpr:
		ident = x.Sel
	default:
		return TestPhaseUnknown // no easy way to get an identifier
	}

	// Find the package containing the called function, even if a named import is used
	obj, _, err := tc.GetIdentDefinition(ident)
	if err != nil {
		slog.Error("Cannot determine if function is an assertion", "ident", ident, "error", err)
		return TestPhaseUnknown
	}

	// Ensure we are dealing with a function call
	function, ok := obj.(*types.Func)
	if !ok {
		return TestPhaseUnknown
	}
	pkg := obj.Pkg()
	if pkg == nil {
		return TestPhaseUnknown // Built-in function
	}

	pkgPath := pkg.Path()

	// Standard library functions
	if pkgPath == "testing" {
		return categorizeStandardTestingMethod(function.Name())
	}

	// Try to detect third-party test harness libraries by shape.
	// An assertion function must interact with a test harness somehow
	if containsTestHarness(function.Type().(*types.Signature)) {
		if phase := categorizeStandardTestingMethod(function.Name()); phase != TestPhaseUnknown {
			return phase
		}
		if isLikelyAssertion(function) {
			return TestPhaseAssert
		}
	}

	return TestPhaseUnknown
}

// Maps standard `testing.TB` methods to their respective test phases.
func categorizeStandardTestingMethod(name string) TestPhase {
	switch name {
	// Check for failure functions
	case "Error", "Errorf", "Fatal", "Fatalf", "Fail", "FailNow":
		return TestPhaseAssert
	// Check for subtest definition
	case "Run":
		return TestPhaseSubtest
	default:
		return TestPhaseUnknown
	}
}

// Determines whether a function represents an assertion function based on semantic and structural heuristics.
// Assumes the function is already identified as involving a test harness.
func isLikelyAssertion(function *types.Func) bool {
	signature, ok := function.Type().(*types.Signature)
	if !ok {
		return false
	}

	// Try to distinguish between assertion functions and other non-assertion functions

	// Check for substring match of keywords within the function name
	// TODO this might be brittle, but not totally sure how else to do it without knowing the internals of the library function
	funcName := strings.ToLower(function.Name())
	assertionKeywords := []string{"assert", "require", "check", "verify", "expect", "test", "should", "must", "error", "fail", "equal", "nil", "true", "false", "len", "panic"}
	for _, keyword := range assertionKeywords {
		if strings.Contains(funcName, keyword) {
			return true
		}
	}

	// Heuristic: check for an `any` or `...any` parameter, e.g. as the value being asserted or messages to be printed on failure
	params := signature.Params()
	for i := range params.Len() {
		param := params.At(i).Type()

		// Regular `any` parameter
		if asttools.IsEmptyInterface(param) {
			return true
		}
		// Variadic `...any` as the last parameter
		if signature.Variadic() && i == params.Len()-1 {
			if slice, ok := param.(*types.Slice); ok {
				if asttools.IsEmptyInterface(slice.Elem()) {
					return true
				}
			}
		}
	}

	return false
}

// Checks if a function signature uses a test harness type through its parameters or receiver,
// based on structural rules rather than relying on package names.
func containsTestHarness(signature *types.Signature) bool {
	// Check if the function is a method of a test harness
	if recv := signature.Recv(); recv != nil {
		if checkForTestHarnessType(recv.Type(), 0) {
			return true
		}
	}
	// Check if the function takes a test harness as a parameter
	for param := range signature.Params().Variables() {
		if checkForTestHarnessType(param.Type(), 0) {
			return true
		}
	}
	return false
}

const maxTestHarnessRecursionDepth = 5

// Recursively checks if a type acts as a testing harness or contains a testing harness as a struct field,
// based on structural rules rather than relying on package names.
func checkForTestHarnessType(t types.Type, depth int) bool {
	if t == nil {
		return false
	}
	// todo add memoization using sync.Map

	// Prevent infinite recursion on cyclic structs (e.g., linked lists)
	if depth > maxTestHarnessRecursionDepth {
		return false
	}

	// Check if the type itself looks like a test runner.
	// Handles *testing.T, interfaces like testify's TestingT, and structs with an embedded harness.
	if isDuckTypedTestHarness(t) {
		return true
	}

	// Recursively check struct fields (both named and anonymous)
	if structType, ok := asttools.UnderlyingType(t).(*types.Struct); ok {
		for field := range structType.Fields() {
			if checkForTestHarnessType(field.Type(), depth+1) {
				return true
			}
		}
	}

	return false
}

// Checks if a type acts like a `testing.T` test harness based on the methods it provides.
func isDuckTypedTestHarness(t types.Type) bool {
	if t == nil {
		return false
	}
	// Get the type's method set, including interface methods and promoted methods from embedded structs.
	mset := types.NewMethodSet(t)

	// Heuristic: test harnesses should at least be able to fail tests
	return mset.Lookup(nil, "Errorf") != nil ||
		mset.Lookup(nil, "Fatalf") != nil ||
		mset.Lookup(nil, "FailNow") != nil
}

// Determines if an expression involves modifying a variable defined outside the loop's scope.
// This may struggle with side effects of function calls, as it only checks the location where the base identifier is defined.
func isExternalMutation(expr dst.Expr, tc *TestCase, loopScope *types.Scope) bool {
	if loopScope == nil || expr == nil {
		return false
	}

	// Find the "base" variable being modified
	ident := asttools.GetRootIdent(expr)
	if ident == nil || ident.Name == "_" {
		return false // Not enough information
	}

	// Find the definition of the variable
	obj := tc.ObjectOf(ident)
	if obj == nil {
		return false // Could not find the definition, but not necessarily because it's external
	}

	// If the variable being modified is not defined in the loop's scope, then it's an external mutation
	if !asttools.IsScopeAncestor(loopScope, obj.Parent()) {
		return true
	}
	return false
}

// ================================================================================================

// LoopType represents the classification of a `for` loop in source code.
type LoopType int

const (
	// Represents that a loop was not detected
	LoopTypeNone LoopType = iota

	// Range loop over a slice/array of structs,   e.g. `for _, s := range []struct{...}`
	LoopTypeRangeStructs

	// Range loop over a slice/array of non-struct elements,   e.g. `for _, x := range []int{...}`
	LoopTypeRangeNonStructs

	// Range loop over a map of keys/values,   e.g. `for k, v := range map[string]int{...}`
	LoopTypeRangeMap

	// Range loop over an integer value directly,   e.g. `for i := range 10`
	LoopTypeRangeInt

	// Range loop over something not categorized above (including strings, channels, or iterators),   e.g. `for _, c := range "hello"`
	LoopTypeRangeOther

	// Three-clause loop using an integer index variable,   e.g. `for i := 0; i < 10; i++`
	LoopTypeIterativeIndexed

	// Three-clause loop that does not use an integer index variable,   e.g. `for p := head; p != nil; p = p.Next`
	LoopTypeIterativeNonIndexed

	// Loop only containing a condition, acting as a while loop,   e.g. `for condition { ... }`
	LoopTypeConditionOnly

	// Infinite loop with no clauses,   e.g. `for { ... }`
	LoopTypeInfinite

	// Loop with an unrecognized structure
	LoopTypeUnknown
)

// Return the string representation of LoopType.
func (lt LoopType) String() string {
	switch lt {
	case LoopTypeRangeStructs:
		return "range structs"
	case LoopTypeRangeNonStructs:
		return "range non-structs"
	case LoopTypeRangeMap:
		return "range map"
	case LoopTypeRangeInt:
		return "range int"
	case LoopTypeRangeOther:
		return "range other"
	case LoopTypeIterativeIndexed:
		return "iterative indexed"
	case LoopTypeIterativeNonIndexed:
		return "iterative non-indexed"
	case LoopTypeConditionOnly:
		return "condition-only"
	case LoopTypeInfinite:
		return "infinite"
	case LoopTypeUnknown:
		return "unknown"
	default:
		return "none"
	}
}

func (lt LoopType) MarshalJSON() ([]byte, error) {
	return json.Marshal(lt.String())
}

func (lt *LoopType) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}
	switch strings.ToLower(str) {
	case "range structs":
		*lt = LoopTypeRangeStructs
	case "range non-structs":
		*lt = LoopTypeRangeNonStructs
	case "range map":
		*lt = LoopTypeRangeMap
	case "range int":
		*lt = LoopTypeRangeInt
	case "range other":
		*lt = LoopTypeRangeOther
	case "iterative indexed":
		*lt = LoopTypeIterativeIndexed
	case "iterative non-indexed":
		*lt = LoopTypeIterativeNonIndexed
	case "condition-only":
		*lt = LoopTypeConditionOnly
	case "infinite":
		*lt = LoopTypeInfinite
	case "unknown":
		*lt = LoopTypeUnknown
	default:
		slog.Warn("Invalid loop type", "type", str)
		*lt = LoopTypeNone
	}
	return nil
}

