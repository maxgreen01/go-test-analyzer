package testcase

import (
	"encoding/json"
	"go/ast"
	"go/types"
	"log/slog"
	"slices"
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

// Detects and analyzes all the loops found in the expanded statements of a test case, including those called in expanded function calls.
func AnalyzeLoops(tc *TestCase, parsedStmts []*ExpandedStatement) *LoopAnalysisResult {
	loopAnalysis := &LoopAnalysisResult{}
	topLevelSeen := make(map[dst.Node]bool)

	// Analyze each top-level statement's ExpandedStatement tree
	for _, expanded := range parsedStmts {
		loops := analyzeStmtLoops(expanded, nil, tc, topLevelSeen)
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
func analyzeStmtLoops(expanded *ExpandedStatement, parentLoop *Loop, tc *TestCase, seen map[dst.Node]bool) []*Loop {
	var topLevelLoops []*Loop // only used if `parentLoop` is nil

	// Walk the expanded statement tree and inspect each non-nil node within each statement
	WalkExpanded(expanded, func(node dst.Node, children []*ExpandedStatement) bool {
		switch n := node.(type) {
		case *dst.RangeStmt, *dst.ForStmt:
			// Ignore loops that have already been analyzed directly under this parent
			if seen[n] {
				return false
			}

			// Analyze the loop and detect any children using recursion
			loop := CreateLoop(n, children, tc)

			// Save nested loops under their parent, or collect top-level loops to return
			if parentLoop != nil {
				parentLoop.NestedLoops = append(parentLoop.NestedLoops, loop)
			} else {
				topLevelLoops = append(topLevelLoops, loop)
			}
			seen[n] = true

			// Stop inspecting descendants because the body has already been processed by `CreateLoop`
			return false

		default:
			// Check function calls (which have already been expanded) and any other non-nil nodes for metadata features
			if parentLoop != nil {
				parentLoop.RegisterNode(node, tc)
			}
		}

		// Continue descending to search for additional loops
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

	// Detected metadata features, embedded because it saves an unnecessary layer of nesting
	FeatureSet

	// Additional loops that are contained within this loop, if any
	NestedLoops []*Loop `json:"nestedLoops,omitempty"`
}

// Creates a Loop instance, and analyzes the loop's inner statements to detect nested loops and metadata features in a depth-first traversal.
//
// Note that `children` is expected to be a superset of the statements actually inside the loop body, since it should hold all the child
// statements of the most recently expanded function call or original statement, so it may include statements next to the loop.
func CreateLoop(stmt dst.Node, children []*ExpandedStatement, tc *TestCase) *Loop {
	loop := &Loop{
		Content: asttools.NodeToString(stmt),
	}
	loop.Delegated = !tc.IsWithinTestFunction(stmt)
	loop.scope = tc.GetNodeScope(stmt)
	if astFuncs, _ := tc.GetEnclosingFunctions(stmt); len(astFuncs) > 0 {
		loop.enclosingFunc = astFuncs[0]
	}

	// Inspect the structure of the loop itself
	var body *dst.BlockStmt
	switch loopStmt := stmt.(type) {
	case *dst.RangeStmt:
		loop.LoopType = classifyRangeLoop(loopStmt, tc)
		body = loopStmt.Body
		// Manually search for nested statements in this loop's range expression, since they aren't checked anywhere else
		analyzeStmtLoops(&ExpandedStatement{Stmt: &dst.ExprStmt{X: loopStmt.X}, Children: children}, loop, tc, make(map[dst.Node]bool))
		analyzeStmtLoops(&ExpandedStatement{Stmt: &dst.ExprStmt{X: loopStmt.Key}, Children: children}, loop, tc, make(map[dst.Node]bool))

	case *dst.ForStmt:
		loop.LoopType = classifyForLoop(loopStmt, tc)
		body = loopStmt.Body
		// Manually search for nested statements in this loop's header clauses, since they aren't checked anywhere else
		analyzeStmtLoops(&ExpandedStatement{Stmt: loopStmt.Init, Children: children}, loop, tc, make(map[dst.Node]bool))
		analyzeStmtLoops(&ExpandedStatement{Stmt: &dst.ExprStmt{X: loopStmt.Cond}, Children: children}, loop, tc, make(map[dst.Node]bool))
		analyzeStmtLoops(&ExpandedStatement{Stmt: loopStmt.Post, Children: children}, loop, tc, make(map[dst.Node]bool))
	}

	// Analyze nested statements using a dummy ExpandedStatement representing the loop body, and reuse the containing statement's children.
	// Use a fresh `seen` map to avoid duplicate loops within this new parent, but allow them to be repeated under different parents.
	// Any detected nested loops or metadata features are automatically attached to this loop.
	analyzeStmtLoops(&ExpandedStatement{Stmt: body, Children: children}, loop, tc, make(map[dst.Node]bool))

	// Make sure all metadata features are propagated and set correctly
	loop.finalize()

	return loop
}

// Bubbles up detected metadata features from nested loops, and populates computed fields based on other analysis results
func (loop *Loop) finalize() {
	// Bubble up features and delegated status from nested loops
	for _, child := range loop.NestedLoops {
		bubbleFeatures(&loop.FeatureSet, &child.FeatureSet)
	}

	// Classify this loop based on its fully bubbled properties
	if loop.HasSubtest.Present || (loop.HasAssertion.Present && !loop.DoesExternalMutation) {
		loop.IndicatesTableDriven = true
	}
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
	indexIdent := GetForStmtIndexIdent(stmt) // todo LATER maybe don't require that this is an ident, which would allow using a struct field (for example) as the index variable
	if indexIdent != nil && tc != nil {
		typ := tc.TypeOf(indexIdent)
		if typ != nil && asttools.IsBasicType(typ, types.IsInteger) {
			return LoopTypeIterativeIndexed
		}
	}

	return LoopTypeIterativeNonIndexed
}

// Represents a metadata feature detected during a supplementary analysis, acting like a regular boolean with an additional flag indicating
// whether the feature could only be detected outside the original test function.
type analysisFeature struct {
	// Whether the feature is present in the analyzed block or any of its children
	Present bool `json:"present"`
	// Whether the feature can only be detected outside the original test function (i.e. could not be found locally inside the original test function)
	Delegated bool `json:"delegated"`
}

// Sets the metadata feature flag to `true` if the feature is present. Also inherits the delegated flag if the feature is first being registered,
// or is set back to `false` if a local version is found later. If it's been found locally, it won't ever be set back to delegated.
func (af *analysisFeature) register(present bool, isDelegated bool) {
	if present {
		// The value of `isDelegated` only gets stored when the feature is first registered, or if the feature was already delegated.
		// If the feature is already delegated, it will stay delegated until a local version is found.
		af.Delegated = (!af.Present || af.Delegated) && isDelegated
		af.Present = true
	}
}

// todo CLEANUP maybe this marshaling logic is too convoluted
// Marshal as "false" for a feature that is not present, or the full struct for a feature that is present.
func (af analysisFeature) MarshalJSON() ([]byte, error) {
	if !af.Present {
		return json.Marshal(false)
	}
	// Marshal the whole struct, avoiding infinite recursion using a copy type.
	// Details:  https://boldlygo.tech/posts/2020-12-12-go-json-self-referencing-marshaler/
	type analysisFeatureCopy analysisFeature
	return json.Marshal(analysisFeatureCopy(af))
}

// FeatureSet groups the standard supplementary analysis features shared by loops and conditional branches.
type FeatureSet struct {
	Delegated            bool            `json:"delegated"`            // True if the block itself is in a helper function, or if any of its features are delegated to helper functions
	HasSubtest           analysisFeature `json:"hasSubtest"`           // Whether the block defines subtests using the built-in `t.Run` method or a library-based equivalent
	HasAssertion         analysisFeature `json:"hasAssertion"`         // Whether the block contains an assertion, detected based on the presence of built-in or library-based test failure functions
	HasEarlyExit         analysisFeature `json:"hasEarlyExit"`         // Whether the block contains a return statement that exits the block's enclosing function. Being delegated only indicates that the block is in a helper function.
	DoesExternalMutation bool            `json:"doesExternalMutation"` // Whether the block directly mutates any data defined outside its own scope. Doesn't have a delegated flag because it's recalculated for each block independently.

	scope         *types.Scope `json:"-"` // The scope corresponding to this block, used for finding external mutations
	enclosingFunc ast.Node     `json:"-"` // The innermost AST function enclosing this block, used for determining whether statements are inside local function literals
}

// RegisterNode inspects a DST node and updates the FeatureSet's features accordingly.
func (fs *FeatureSet) RegisterNode(node dst.Node, tc *TestCase) {
	isDelegated := fs.Delegated || !tc.IsWithinTestFunction(node)
	switch n := node.(type) {
	case *dst.CallExpr:
		phase := categorizeCallExpr(n, tc)
		fs.HasSubtest.register(phase == TestPhaseSubtest, isDelegated)
		fs.HasAssertion.register(phase == TestPhaseAssert, isDelegated)
		// Note: `panic` calls would be detected here (or in categorizeCallExpr)
	case *dst.AssignStmt:
		for _, lhs := range n.Lhs {
			if isExternalMutation(lhs, tc, fs.scope) {
				fs.DoesExternalMutation = true
			}
		}
	case *dst.IncDecStmt:
		if isExternalMutation(n.X, tc, fs.scope) {
			fs.DoesExternalMutation = true
		}
	case *dst.ReturnStmt:
		// A return statement is considered an early exit if it exits the same function enclosing the relevant block itself.
		// By contrast, a return statement in a helper function would not exit the function where the block is located.
		// Note: in this case, being delegated is just a synonym for the block itself being inside a helper function.
		// A return statement that is "delegated" in the usual sense fundamentally doesn't make sense, because return
		// statements can't be used to exit a function from within a different function.
		astFuncs, _ := tc.GetEnclosingFunctions(n)
		if fs.enclosingFunc != nil && len(astFuncs) > 0 && astFuncs[0] == fs.enclosingFunc {
			fs.HasEarlyExit.register(true, isDelegated)
		}
	}
	// todo LATER - we currently don't track `break` or `continue` statements for HasEarlyExit because determining when to bubble up would involve
	//   tracking each block's parent statements (especially with nested loops, switch statements, and labels), and this isn't easily generalizable
	//   for both loops and conditionals since the boundaries of these statements' impact is closely related to the concept of nested loops, but
	//   not with nested conditionals
}

// Bubbles up any detected features from a child to its parent, and updates the parent's delegated status accordingly.
// This modifies the parent in-place, and should be called from bottom up on the FeatureSet tree to propagate correctly.
func bubbleFeatures(parent, child *FeatureSet) {
	// Bubble up any `Present` features, inheriting the delegated status of the entire child or the individual feature
	parent.HasSubtest.register(child.HasSubtest.Present, child.Delegated || child.HasSubtest.Delegated)
	parent.HasAssertion.register(child.HasAssertion.Present, child.Delegated || child.HasAssertion.Delegated)

	// Only bubble up HasEarlyExit if the parent and child are within the same innermost enclosing function,
	// since an early exit inside a helper function doesn't exit the parent's function
	if parent.enclosingFunc == child.enclosingFunc {
		parent.HasEarlyExit.register(child.HasEarlyExit.Present, child.Delegated || child.HasEarlyExit.Delegated)
	}

	// Note: DoesExternalMutation is never bubbled up because child blocks may mutate variables defined in the parent, which is not
	// considered an external mutation for the parent block. This means DoesExternalMutation is recalculated for each block independently,
	// which works because the assignments that happen inside the child are checked again during the parent's own analysis.

	// If the block is located inside the test function (not delegated already), it should become delegated if
	// any of its features are delegated, since they've already undergone all the relevant logic
	parentFeats := []analysisFeature{parent.HasSubtest, parent.HasAssertion, parent.HasEarlyExit}
	anyFeaturesDelegated := slices.ContainsFunc(parentFeats, func(af analysisFeature) bool {
		return af.Present && af.Delegated
	})
	parent.Delegated = parent.Delegated || anyFeaturesDelegated
}

// TestPhase is used to categorize test statements into the test phases.
// TODO this is very basic & temporary/underused for now
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
		slog.Warn("Could not find definition of function call being categorized", "ident", ident, "error", err)
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
	// todo add memoization using sync.Map - make sure to guard the key like with findDefinitionMemo

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

	// If the modifying expression and variable declaration are located inside the same innermost function, the mutation
	// is local to that helper and is therefore not external, even though it's outside the loop's direct scope. This
	// doesn't apply when they're in both in the test function itself, since that would follow the normal scoping check.
	if exprFuncs, _ := tc.GetEnclosingFunctions(expr); len(exprFuncs) > 0 {
		if declFuncs, _ := asttools.GetEnclosingFunctions(obj.Pos(), tc.GetPackageFiles()); len(declFuncs) > 0 {
			exprEnclosing := tc.AstToDst(exprFuncs[0])
			declEnclosing := tc.AstToDst(declFuncs[0])
			if exprEnclosing == declEnclosing && exprEnclosing != tc.funcDecl {
				return false
			}
		}
	}

	// If the variable being modified is not defined somewhere within the loop's scope, then it's an external mutation
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
