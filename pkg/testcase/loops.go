package testcase

import (
	"encoding/json"
	"go/types"
	"log/slog"
	"slices"
	"strings"

	"github.com/dave/dst"
	"github.com/maxgreen01/go-test-analyzer/pkg/asttools"
)

// Represents the result of analyzing the loops found in a test case.
type LoopAnalysisResult struct {
	// The number of detected direct loops, not including nested loops
	NumDirectLoops int `json:"numDirectLoops"`

	// The number of detected indirect loops, not including nested loops
	NumIndirectLoops int `json:"numIndirectLoops"`

	// Detected loops that are directly present in the test function
	DirectLoops []Loop `json:"directLoops"`

	// Detected loops that are present in helper functions called by the test function, including those within nested helper functions
	IndirectLoops []Loop `json:"indirectLoops"`
}

// Detects and analyzes all the loops found in the expanded statements of a test case
func AnalyzeLoops(tc *TestCase, parsedStmts []*ExpandedStatement) *LoopAnalysisResult {
	loopAnalysis := &LoopAnalysisResult{}

	// Avoid duplicate detections using a hash set to track loops that have already been visited.
	// We use one set instead of separate direct/indirect ones to avoid double-counting loops inside function literals.
	// In this situation, the loop would always be counted as direct before being visited as indirect.
	visited := make(map[dst.Node]struct{})

	for _, expanded := range parsedStmts {
		if expanded == nil {
			continue
		}

		// Detect direct loops from the unexpanded (root) statements
		direct := analyzeLoopsInNode(expanded.Stmt, tc, visited)
		loopAnalysis.DirectLoops = append(loopAnalysis.DirectLoops, direct...)
		loopAnalysis.NumDirectLoops += len(direct)

		// Detect indirect loops using the expanded statements, not including the root (to avoid double-counting direct loops)
		for _, child := range expanded.Children {
			for stmt := range child.All() {
				indirect := analyzeLoopsInNode(stmt, tc, visited)
				loopAnalysis.IndirectLoops = append(loopAnalysis.IndirectLoops, indirect...)
				loopAnalysis.NumIndirectLoops += len(indirect)
			}
		}
	}

	return loopAnalysis
}

// Returns a list of all detected loops, both direct and indirect.
func (loopAnalysis *LoopAnalysisResult) GetAllLoops() []Loop {
	return slices.Concat(loopAnalysis.DirectLoops, loopAnalysis.IndirectLoops)
}

// Returns the number of detected loops that are indicative of a table-driven test
func (loopAnalysis *LoopAnalysisResult) CountTableDriven() int {
	count := 0
	for _, loop := range loopAnalysis.GetAllLoops() {
		if loop.IndicatesTableDriven {
			count++
		}
	}
	return count
}

// Recursively detect loops inside a node based on the DST structure and type information, structuring nested loops hierarchically.
// Ignores nodes that have already been visited to avoid double-counting.
func analyzeLoopsInNode(node dst.Node, tc *TestCase, visited map[dst.Node]struct{}) []Loop {
	if node == nil {
		return nil
	}
	var loops []Loop

	dst.Inspect(node, func(n dst.Node) bool {
		if n == nil {
			return false
		}
		if _, ok := visited[n]; ok {
			return false // Skip already visited nodes
		}

		// Check if the node is a loop
		switch n.(type) {
		case *dst.RangeStmt, *dst.ForStmt:
			visited[n] = struct{}{} // Mark the node as visited
			loops = append(loops, CreateLoop(n, tc, visited))
			return false // Stop inspecting descendants because nested loops have already been handled
		}

		// Not a loop, so continue descending
		return true
	})
	return loops
}

// TODO maybe add a way to save each Loop as a distinct CSV row, but this would be annoying to implement given the inherent split
//   with the AnalysisResult (which contains all the identifying/location information) and the optional nature of the loop analysis in the first place

// ================================================================================================

// Represents a loop statement detected as part of a test case.
type Loop struct {
	// The detected loop structure
	LoopType LoopType `json:"loopType"`

	// The DST code of the loop itself, converted to a string
	Content string `json:"content"`

	// Whether the loop is indicative of a table-driven test
	IndicatesTableDriven bool `json:"indicatesTableDriven"`

	// Whether the loop defines subtests using the built-in `t.Run` method or a library-based equivalent.
	HasSubtest bool `json:"hasSubtest"`

	// Whether the loop contains an assertion, detected based on the presence of built-in or library-based test failure functions
	HasAssertion bool `json:"hasAssertion"`

	// Whether the loop directly mutates any data defined outside the loop
	DoesExternalMutation bool `json:"doesExternalMutation"`

	// Additional loops that are contained within this loop, if any
	NestedLoops []Loop `json:"nestedLoops,omitempty"`
}

// Creates a Loop instance based on the provided DST node, the TestCase that uses it, and list of visited loops.
// Processes nested Loops before analyzing this one, i.e. in a depth-first traversal.
func CreateLoop(stmt dst.Node, tc *TestCase, visited map[dst.Node]struct{}) Loop {
	loop := Loop{Content: asttools.NodeToString(stmt)}

	switch s := stmt.(type) {
	case *dst.RangeStmt:
		loop.LoopType = classifyRangeLoop(s, tc)
		loop.NestedLoops = analyzeLoopsInNode(s.Body, tc, visited)
	case *dst.ForStmt:
		loop.LoopType = classifyForLoop(s, tc)
		loop.NestedLoops = analyzeLoopsInNode(s.Body, tc, visited)
	}

	loop.analyze(stmt, tc)

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

// Perform additional analysis based on the statements inside the loop, populating the corresponding fields
func (loop *Loop) analyze(loopStmt dst.Node, tc *TestCase) {
	if loop == nil || loopStmt == nil || tc == nil {
		return
	}

	// Bubble up flags from children if any of them are set to `true`
	for _, nested := range loop.NestedLoops {
		loop.HasSubtest = loop.HasSubtest || nested.HasSubtest
		loop.HasAssertion = loop.HasAssertion || nested.HasAssertion
		// DoesExternalMutation is not bubbled up because child loops may mutate variables defined in the parent loop, which is not considered an external mutation for the parent loop.
		// This means DoesExternalMutation is recalculated for each loop independently, and the children's assignments are already included in the parent's analysis.
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

	// Check for subtest and assertion function calls in the loop body
	// TODO maybe use ExpandedStatement here to detect calls in helper funcs that don't themselves have loops?
	dst.Inspect(body, func(n dst.Node) bool {
		if loop.HasSubtest && loop.HasAssertion {
			return false // Stop inspecting if both flags are already set
		}

		if callExpr, ok := n.(*dst.CallExpr); ok {
			phase := categorizeCallExpr(callExpr, tc)

			if phase == TestPhaseSubtest {
				loop.HasSubtest = true
			}
			if phase == TestPhaseAssert {
				loop.HasAssertion = true
			}
		}
		// Always keep inspecting to ensure we inspect the full depth, e.g. to find assertions inside a subtest
		return true
	})

	// Check for mutations via assignments and increment/decrement statements on variables outside the loop scope
	if !loop.DoesExternalMutation {
		for _, lhs := range asttools.FindModifiedExpressions(body) {
			if isExternalMutation(lhs, tc, loopScope) {
				loop.DoesExternalMutation = true
				break
			}
		}
	}

	// Determine computed fields based on the other analysis results

	// Check if the loop looks table-driven
	if loop.HasSubtest || (loop.HasAssertion && !loop.DoesExternalMutation) {
		// todo maybe add check on LoopType -- don't want to be overly restrictive and avoid new patterns
		// already-supported are:   []LoopType{LoopTypeRangeStructs, LoopTypeRangeNonStructs, LoopTypeRangeMap, LoopTypeRangeInt, LoopTypeIterativeIndexed}
		loop.IndicatesTableDriven = true
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
