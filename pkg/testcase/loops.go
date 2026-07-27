package testcase

import (
	"encoding/json"
	"go/ast"
	"go/types"
	"log/slog"
	"strings"

	"github.com/dave/dst"
	"github.com/maxgreen01/go-test-analyzer/pkg/asttools"
	"github.com/maxgreen01/go-test-analyzer/pkg/features"
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

// Detects and analyzes all the loops found in the expanded statements of a test case,
// including loops found in expanded function calls.
func AnalyzeLoops(tc *TestCase, parsedStmts []*ExpandedStatement) *LoopAnalysisResult {
	loopAnalysis := &LoopAnalysisResult{}
	topLevelSeen := make(map[dst.Node]bool)
	
	cfa := &controlFlowAnalyzer{
		tc:           tc,
		analyzeLoops: true,
	}
	
	// Analyze each top-level statement's ExpandedStatement tree
	for _, expanded := range parsedStmts {
		cfs := cfa.analyze(expanded, topLevelSeen)
		loopAnalysis.Loops = append(loopAnalysis.Loops, filterTyped[*Loop](cfs)...)
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

	// Total number of statements in this loop, including inside nested statements
	TotalLength int `json:"totalLength"`

	// Detected metadata features, embedded because it saves an unnecessary layer of nesting
	features.FeatureSet

	// Additional control flow statements that are contained within this loop, if any
	NestedStatements []ControlFlowStatement `json:"nestedStatements,omitempty"`
}

// Compile-time interface check
var _ ControlFlowStatement = (*Loop)(nil)

// Creates a Loop instance, and analyzes the loop's inner statements in a depth-first traversal to detect nested control flow statements
// and metadata features.
func CreateLoop(stmt dst.Node, cfa *controlFlowAnalyzer) *Loop {
	var enclosingFunc ast.Node
	if astFuncs, _ := cfa.tc.GetEnclosingFunctions(stmt); len(astFuncs) > 0 {
		enclosingFunc = astFuncs[0]
	}
	loop := &Loop{
		Content:    asttools.NodeToString(stmt),
		FeatureSet: features.NewFeatureSet(cfa.tc.GetNodeScope(stmt), enclosingFunc, !cfa.tc.IsWithinTestFunction(stmt)),
	}

	// Classify the structure of the loop, and search for nested statements in the loop header and body.
	var body *dst.BlockStmt
	switch loopStmt := stmt.(type) {
	case *dst.RangeStmt:
		loop.LoopType = classifyRangeLoop(loopStmt, cfa.tc)

		// Manually search for nested statements
		body = loopStmt.Body
		loop.NestedStatements = cfa.analyzeNested([]dst.Node{loopStmt.Key, loopStmt.Value, loopStmt.X, body}, &loop.FeatureSet)

	case *dst.ForStmt:
		loop.LoopType = classifyForLoop(loopStmt, cfa.tc)

		// Manually search for nested statements
		body = loopStmt.Body
		loop.NestedStatements = cfa.analyzeNested([]dst.Node{loopStmt.Init, loopStmt.Cond, loopStmt.Post, body}, &loop.FeatureSet)
	}

	if body != nil {
		loop.Length = len(body.List)
	}

	// Bubble up detected metadata features from nested control flow statements
	BubbleUpFeatures(&loop.FeatureSet, loop.NestedStatements)

	// Populate computed fields based on other analysis results
	if loop.HasSubtest.Present || (loop.HasAssertion.Present && !loop.DoesExternalMutation) {
		loop.IndicatesTableDriven = true
	}
	loop.TotalLength = TotalLength(loop)

	return loop
}

// GetNestedStmts returns the nested control flow statements within this statement.
func (loop *Loop) GetNestedStmts() []ControlFlowStatement {
	return loop.NestedStatements
}

// GetFeatureSets returns the metadata feature sets associated with this control flow statement.
func (loop *Loop) GetFeatureSets() []features.FeatureSet {
	return []features.FeatureSet{loop.FeatureSet}
}

// GetLength returns the number of DST statements inside this control flow statement, NOT including nested statements.
func (loop *Loop) GetLength() int {
	return loop.Length
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

// ================================================================================================
// TODO SOON - move this whole section to `features` package once we can separate it from TestCase circular dependency

// RegisterNode inspects a DST node and updates the FeatureSet's features accordingly.
// TODO SOON - make this back into a FeatureSet method once we can separate it from TestCase circular dependency
// TODO maybe rename to something more descriptive than "Register", e.g. "CheckNode"
func RegisterNode(fs *features.FeatureSet, node dst.Node, tc *TestCase) {
	isDelegated := fs.Delegated || !tc.IsWithinTestFunction(node)
	switch n := node.(type) {
	case *dst.CallExpr:
		phase := categorizeCallExpr(n, tc)
		fs.HasSubtest.Register(phase == features.TestPhaseSubtest, isDelegated)
		fs.HasAssertion.Register(phase == features.TestPhaseAssert, isDelegated)
		// Note: `panic` calls would be detected here (or in categorizeCallExpr)
	case *dst.AssignStmt:
		for _, lhs := range n.Lhs {
			if isExternalMutation(lhs, tc, fs.Scope) {
				fs.DoesExternalMutation = true
			}
		}
	case *dst.IncDecStmt:
		if isExternalMutation(n.X, tc, fs.Scope) {
			fs.DoesExternalMutation = true
		}
	case *dst.ReturnStmt:
		// A return statement is considered an early exit if it exits the same function enclosing the relevant block itself.
		// By contrast, a return statement in a helper function would not exit the function where the block is located.
		// Note: in this case, being delegated is just a synonym for the block itself being inside a helper function.
		// A return statement that is "delegated" in the usual sense fundamentally doesn't make sense, because return
		// statements can't be used to exit a function from within a different function.
		astFuncs, _ := tc.GetEnclosingFunctions(n)
		if fs.EnclosingFunc != nil && len(astFuncs) > 0 && astFuncs[0] == fs.EnclosingFunc {
			fs.HasEarlyExit.Register(true, isDelegated)
		}
	}
	// todo LATER - we currently don't track `break` or `continue` statements for HasEarlyExit because determining when to bubble up would involve
	//   tracking each block's parent statements (especially with nested loops, switch statements, and labels), and this isn't easily generalizable
	//   for both loops and conditionals since the boundaries of these statements' impact is closely related to the concept of nested loops, but
	//   not with nested conditionals
}

// Determine which test phase a call expression falls into.
// TODO make this more robust and return fewer "unknown"
func categorizeCallExpr(callExpr *dst.CallExpr, tc *TestCase) features.TestPhase {
	if tc == nil {
		return features.TestPhaseUnknown
	}

	// Get the identifier holding the name of the function being called
	var ident *dst.Ident
	switch x := callExpr.Fun.(type) {
	case *dst.Ident:
		ident = x
	case *dst.SelectorExpr:
		ident = x.Sel
	default:
		return features.TestPhaseUnknown // no easy way to get an identifier
	}

	// Find the package containing the called function, even if a named import is used
	obj, _, err := tc.GetIdentDefinition(ident)
	if err != nil {
		slog.Warn("Could not find definition of function call being categorized", "ident", ident, "error", err)
		return features.TestPhaseUnknown
	}

	// Ensure we are dealing with a function call
	function, ok := obj.(*types.Func)
	if !ok {
		return features.TestPhaseUnknown
	}
	pkg := obj.Pkg()
	if pkg == nil {
		return features.TestPhaseUnknown // Built-in function
	}

	pkgPath := pkg.Path()

	// Standard library functions
	if pkgPath == "testing" {
		return features.CategorizeStandardTestingMethod(function.Name())
	}

	// Try to detect third-party test harness libraries by shape.
	// An assertion function must interact with a test harness somehow
	if features.ContainsTestHarness(function.Type().(*types.Signature)) {
		if phase := features.CategorizeStandardTestingMethod(function.Name()); phase != features.TestPhaseUnknown {
			return phase
		}
		if features.IsLikelyAssertion(function) {
			return features.TestPhaseAssert
		}
	}

	return features.TestPhaseUnknown
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
