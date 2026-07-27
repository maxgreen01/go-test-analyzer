package testcase

import (
	"encoding/json"
	"go/ast"
	"go/token"
	"go/types"
	"slices"

	"github.com/dave/dst"
	"github.com/maxgreen01/go-test-analyzer/pkg/asttools"
	"github.com/maxgreen01/go-test-analyzer/pkg/features"
)

// IfElseAnalysisResult represents the result analyzing the if/else statements in a table-driven test.
// todo could probably add more useful computed fields here
type IfElseAnalysisResult struct {
	NumConditionals int       `json:"numConditionals"` // Number of detected conditionals, not including nested ones
	Conditionals    []*IfStmt `json:"conditionals"`    // Detected conditionals found in the test case, with nested conditionals stored internally
}

// Detects and analyzes all conditionals found inside the scenario runner loop of a table-driven test case,
// including conditionals found in expanded function calls.
// todo LATER - maybe add support for detecting switch statements
func AnalyzeConditionals(tc *TestCase, scenarioSet *ScenarioSet, parsedStmts []*ExpandedStatement) *IfElseAnalysisResult {
	result := &IfElseAnalysisResult{}

	cfs := analyzeRunnerControlFlow(tc, scenarioSet, parsedStmts, true, false)
	result.Conditionals = append(result.Conditionals, filterTyped[*IfStmt](cfs)...)

	result.NumConditionals = len(result.Conditionals)

	return result
}

// ================================================================================================

// IfStmt represents an entire if/else chain detected in a table-driven test.
type IfStmt struct {
	Content     string      `json:"content"`     // The DST code of the full statement, converted to a string
	TotalLength int         `json:"totalLength"` // Total number of statements in this if/else chain, including inside nested statements
	InHelper    bool        `json:"inHelper"`    // True if the statement is physically located in a helper function outside the test function
	Clauses     []*IfClause `json:"clauses"`     // List of all branches in the statement
}

// Compile-time interface check
var _ ControlFlowStatement = (*IfStmt)(nil)

// Creates an IfStmt instance representing an entire if/else chain, and analyzes the clause's inner statements in a depth-first traversal
// to detect nested control flow statements and metadata features.
func CreateIfStmt(stmt *dst.IfStmt, cfa *controlFlowAnalyzer) *IfStmt {
	ifStmt := &IfStmt{
		Content:  asttools.NodeToString(stmt),
		InHelper: !cfa.tc.IsWithinTestFunction(stmt),
	}

	// Convert nested DST "else" branches into a flat slice of IfClause elements, and analyze the statements within each clause

	// Track the current clause, where `curr` should be an IfStmt ("then" or "else if"), BlockStmt ("else"),
	// or nil (end of chain w/o "else").
	curr := stmt
	for curr != nil {
		// The first IfStmt clause is considered the "then" clause, and subsequent ones are "else if"
		clauseType := IfClauseTypeElseIf
		if curr == stmt {
			clauseType = IfClauseTypeThen
		}

		ifClause := CreateIfClause(curr, clauseType, cfa)
		ifStmt.Clauses = append(ifStmt.Clauses, ifClause)

		// Move to the next clause in the chain
		if nextIf, ok := curr.Else.(*dst.IfStmt); ok {
			// Another "else if"
			curr = nextIf

		} else if elseBlock, ok := curr.Else.(*dst.BlockStmt); ok {
			// Finish looping with the "else" clause
			elseClause := CreateIfClause(elseBlock, IfClauseTypeElse, cfa)
			ifStmt.Clauses = append(ifStmt.Clauses, elseClause)
			break
		} else {
			// No more clauses in the chain, so finish looping
			break
		}
	}

	// Compute calculated fields based on finished clauses
	ifStmt.TotalLength = TotalLength(ifStmt)

	return ifStmt
}

// GetNestedStmts returns the nested control flow statements within this statement.
func (ifs *IfStmt) GetNestedStmts() []ControlFlowStatement {
	var nested []ControlFlowStatement
	for _, clause := range ifs.Clauses {
		nested = append(nested, clause.NestedStatements...)
	}
	return nested
}

// GetFeatureSets returns the metadata feature sets associated with this control flow statement.
func (ifs *IfStmt) GetFeatureSets() []features.FeatureSet {
	var featureSets []features.FeatureSet
	for _, clause := range ifs.Clauses {
		featureSets = append(featureSets, clause.FeatureSet)
	}
	return featureSets
}

// GetLength returns the number of DST statements inside this control flow statement, NOT including nested statements.
func (ifs *IfStmt) GetLength() int {
	// Excluding nested statements, the length of an if/else chain is just the sum of the lengths of its clauses
	length := 0
	for _, clause := range ifs.Clauses {
		length += clause.Length
	}
	return length
}

// IfClause represents one branch of a if/else chain detected in a table-driven test.
type IfClause struct {
	Type                IfClauseType           `json:"type"`                // Type of this clause with respect to the if/else chain (then, else if, else)
	Condition           string                 `json:"condition,omitempty"` // String representation of the condition expression, if any
	Variables           []*IfVarBehavior       `json:"variables,omitempty"` // Variables and fields used in the condition, if any
	features.FeatureSet                        // Detected metadata features, embedded because it saves an unnecessary layer of nesting
	NestedStatements    []ControlFlowStatement `json:"nestedStatements,omitempty"` // Additional control flow statements that are contained within this branch, if any
}

// Creates an IfClause instance representing a single branch of an if/else chain, and analyzes the clause's inner statements in a depth-first
// traversal to detect nested control flow statements and metadata features.
func CreateIfClause(clauseStmt dst.Stmt, clauseType IfClauseType, cfa *controlFlowAnalyzer) *IfClause {
	var enclosingFunc ast.Node
	if astFuncs, _ := cfa.tc.GetEnclosingFunctions(clauseStmt); len(astFuncs) > 0 {
		enclosingFunc = astFuncs[0]
	}
	clause := &IfClause{
		Type:       clauseType,
		FeatureSet: features.NewFeatureSet(cfa.tc.GetNodeScope(clauseStmt), enclosingFunc, !cfa.tc.IsWithinTestFunction(clauseStmt)),
	}

	// Search for nested statements in the clause, and process extra fields for a "then" or "else if" clause
	// compared to an "else" clause
	var body *dst.BlockStmt
	switch stmt := clauseStmt.(type) {
	case *dst.IfStmt:
		// Analyze variables in the condition expression
		clause.Condition = asttools.NodeToString(stmt.Cond)
		if stmt.Init != nil {
			clause.Condition = asttools.NodeToString(stmt.Init) + "; " + clause.Condition
		}
		clause.Variables = analyzeCondition(stmt, cfa.tc, cfa.scenarioType, cfa.loopScope)

		// Manually search for nested statements
		body = stmt.Body
		clause.NestedStatements = cfa.analyzeNested([]dst.Node{stmt.Init, stmt.Cond, body}, &clause.FeatureSet)

	case *dst.BlockStmt:
		// Entire "else" clause statement is just the body
		body = stmt
		clause.NestedStatements = cfa.analyzeNested([]dst.Node{body}, &clause.FeatureSet)
	}

	if body != nil {
		clause.Length = len(body.List)
	}

	// Bubble up detected metadata features from nested control flow statements
	BubbleUpFeatures(&clause.FeatureSet, clause.NestedStatements)

	return clause
}

// IfVarBehavior stores behavior details and source scope for a variable in a condition.
type IfVarBehavior struct {
	Name   string       `json:"name"`             // Name of the variable, e.g., `tc.Value`, `localVal[i]`, `err`
	Source IfVarSource  `json:"source"`           // Classification of the location where the variable is defined
	Usages []IfVarUsage `json:"usages,omitempty"` // Classifications of the ways the variable is used in the condition
}

// Analyzes a conditional's header (including the initializer) to extract any variables that are used and classify their behavior.
func analyzeCondition(stmt *dst.IfStmt, tc *TestCase, scenarioType types.Type, loopScope *types.Scope) []*IfVarBehavior {
	var behaviors []*IfVarBehavior
	ifScope := tc.GetNodeScope(stmt)

	dst.Inspect(stmt, func(node dst.Node) bool {
		if node == nil || node == stmt.Body || node == stmt.Else {
			// Inspect the init statement and condition, but not the body or else clause
			return false
		}

		// Search for variables
		var ident *dst.Ident
		switch x := node.(type) {
		case *dst.Ident:
			ident = x
		case *dst.SelectorExpr:
			ident = x.Sel
		default:
			return true
		}

		// Only analyze variables, parameters, results, struct fields, or constants (filtering out builtins)
		var target dst.Expr
		if obj := tc.ObjectOf(ident); obj != nil && obj.Pkg() != nil {
			switch obj.(type) {
			case *types.Var, *types.Const:
				target = node.(dst.Expr)
			}
		}
		if target == nil {
			return true // No applicable variable to analyze, so move on
		}
		if ident, ok := target.(*dst.Ident); ok && ident.Name == "_" {
			return true // Ignore blank identifier
		}

		// Find the variable behavior struct corresponding to this variable (reused across identifier instances), or create a new one
		var behavior *IfVarBehavior
		name := asttools.NodeToString(target)
		if idx := slices.IndexFunc(behaviors, func(b *IfVarBehavior) bool { return b.Name == name }); idx >= 0 {
			behavior = behaviors[idx]
		} else {
			source := classifyVarSource(target, tc, scenarioType, ifScope, loopScope)
			behavior = &IfVarBehavior{Name: name, Source: source}
			behaviors = append(behaviors, behavior)
		}

		// Classify the way this specific variable instance is used in either the `Init` or `Cond` of the statement,
		// and add it to the variable behavior struct if it's not already present
		usage := classifyVarUsage(target, stmt)
		if usage != IfVarUsageNone && !slices.Contains(behavior.Usages, usage) {
			behavior.Usages = append(behavior.Usages, usage)
		}

		if _, ok := target.(*dst.SelectorExpr); ok {
			// We analyzed the selector as a unit already, so don't separately analyze its children too
			return false
		}

		return true
	})

	return behaviors
}

// Determines the scope where a given variable was defined.
func classifyVarSource(expr dst.Expr, tc *TestCase, scenarioType types.Type, ifScope *types.Scope, loopScope *types.Scope) IfVarSource {
	if expr == nil || tc == nil {
		return IfVarSourceUnknown
	}

	if isScenarioFieldReference(expr, tc, scenarioType) {
		return IfVarSourceScenario
	}

	ident := asttools.GetRootIdent(expr)
	if ident == nil {
		return IfVarSourceUnknown
	}

	obj := tc.ObjectOf(ident)
	if obj == nil {
		return IfVarSourceUnknown
	}

	if ifScope != nil && obj.Parent() == ifScope {
		return IfVarSourceIfScope
	}

	if loopScope != nil && asttools.IsScopeAncestor(loopScope, obj.Parent()) {
		return IfVarSourceLoopScope
	}

	funcScope := tc.GetFunctionScope()
	if funcScope != nil && asttools.IsScopeAncestor(funcScope, obj.Parent()) {
		return IfVarSourceTestScope
	}

	return IfVarSourcePackage
}

// Determines whether an expression refers to a field on the given scenario object.
func isScenarioFieldReference(expr dst.Expr, tc *TestCase, scenarioType types.Type) bool {
	if expr == nil || scenarioType == nil || tc == nil {
		return false
	}

	// todo cleanup - could maybe return the unwrapped expression as the variable struct's `name` field
	unwrapped := expr
	for {
		if paren, ok := unwrapped.(*dst.ParenExpr); ok {
			unwrapped = paren.X
		} else if star, ok := unwrapped.(*dst.StarExpr); ok {
			unwrapped = star.X
		} else if unary, ok := unwrapped.(*dst.UnaryExpr); ok {
			unwrapped = unary.X
		} else {
			break
		}
	}

	// Get a reference to the variable that might be the scenario object
	var structExpr dst.Expr
	if selectorExpr, ok := unwrapped.(*dst.SelectorExpr); ok {
		structExpr = selectorExpr.X // get the LHS of the selector (e.g., `tt` in `tt.field`)
	} else if ident, ok := unwrapped.(*dst.Ident); ok {
		structExpr = ident // this ident might be for the scenario object itself
	} else {
		return false
	}

	// The original expression is a scenario field reference if the type of the structExpr matches the scenario type
	// todo CLEANUP we could maybe be more accurate here by checking the struct's field names against `selectorExpr.Sel`
	if typ := tc.TypeOf(structExpr); typ != nil {
		if types.Identical(asttools.Unpointer(typ), asttools.Unpointer(scenarioType)) {
			return true
		}
	}
	return false
}

// Determines how a variable or field expression is used inside a conditional's header.
func classifyVarUsage(target dst.Expr, stmt *dst.IfStmt) IfVarUsage {
	// Helper function to check if a node matches the target by unwrapping parentheses and stripping references & dereferences.
	// Use strict equality comparison so we only match the one target node exactly.
	isTarget := func(node dst.Node) bool {
		unwrapped := node
		for {
			if paren, ok := unwrapped.(*dst.ParenExpr); ok {
				unwrapped = paren.X
			} else if star, ok := unwrapped.(*dst.StarExpr); ok {
				unwrapped = star.X // strip dereference operator
			} else if unary, ok := unwrapped.(*dst.UnaryExpr); ok && unary.Op == token.AND {
				unwrapped = unary.X // strip address-of operator
			} else {
				break
			}
		}
		return unwrapped == target
	}

	// Check for `if target` outside the main inspection to avoid potential edge cases where the
	// target is nested inside an expression that didn't match the expected patterns below.
	if isTarget(stmt.Cond) {
		return IfVarUsageDirectCheck
	}

	result := IfVarUsageOther
	dst.Inspect(stmt, func(node dst.Node) bool {
		if node == nil || result != IfVarUsageOther || node == stmt.Body || node == stmt.Else {
			// Inspect the init statement and condition (with short-circuiting when a result is found),
			// but don't inspect the body or else clause
			return false
		}

		switch n := node.(type) {
		case *dst.AssignStmt:
			for _, lhs := range n.Lhs {
				if isTarget(lhs) {
					result = IfVarUsageVariableCreation
					return false
				}
			}
		case *dst.UnaryExpr:
			if n.Op == token.NOT && isTarget(n.X) {
				result = IfVarUsageDirectCheck
				return false
			} else if n.Op != token.AND && isTarget(n.X) {
				result = IfVarUsageComputation
				return false
			}
		case *dst.BinaryExpr:
			switch n.Op {
			case token.LAND, token.LOR:
				if isTarget(n.X) || isTarget(n.Y) {
					result = IfVarUsageDirectCheck
					return false
				}
			case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
				if isTarget(n.X) || isTarget(n.Y) {
					result = IfVarUsageComparison
					return false
				}
			default: // according to the Go spec, the only other binary operators are addition or multiplication operators
				if isTarget(n.X) || isTarget(n.Y) {
					result = IfVarUsageComputation
					return false
				}
			}
		case *dst.CallExpr:
			// Check if target is the receiver of the method call (e.g. `val.IsValid()`)
			if sel, ok := n.Fun.(*dst.SelectorExpr); ok && isTarget(sel.X) {
				result = IfVarUsageMethodReceiver
				return false
			}
			// Check if target is the function name
			if isTarget(n.Fun) {
				result = IfVarUsageFunctionCall
				return false
			}
			// Check if target is a function argument
			for _, arg := range n.Args {
				if isTarget(arg) {
					result = IfVarUsageFunctionArg
					return false
				}
			}
		}
		return true
	})

	return result
}

// ================================================================================================

// IfClauseType represents the category of a conditional branch.
type IfClauseType int

const (
	IfClauseTypeNone   IfClauseType = iota
	IfClauseTypeThen                // The "then" block of an if statement
	IfClauseTypeElseIf              // An "else if" block in a chain
	IfClauseTypeElse                // An "else" block in a chain
)

func (ict IfClauseType) String() string {
	switch ict {
	case IfClauseTypeThen:
		return "then"
	case IfClauseTypeElseIf:
		return "else if"
	case IfClauseTypeElse:
		return "else"
	default:
		return "none"
	}
}

func (ict IfClauseType) MarshalJSON() ([]byte, error) {
	return json.Marshal(ict.String())
}

func (ict *IfClauseType) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}
	switch str {
	case "then":
		*ict = IfClauseTypeThen
	case "else if":
		*ict = IfClauseTypeElseIf
	case "else":
		*ict = IfClauseTypeElse
	default:
		*ict = IfClauseTypeNone
	}
	return nil
}

// ================================================================================================

// IfVarSource represents the origin scope of a variable or field used in a conditional statement.
type IfVarSource int

const (
	IfVarSourceUnknown   IfVarSource = iota
	IfVarSourceScenario              // Part of the table-driven scenario object,   e.g. `tt.field` or `tt` itself
	IfVarSourceIfScope               // Defined within the initialization statement of the if/else clause,   e.g. in `if val := helper(); val > 0`
	IfVarSourceLoopScope             // Defined within the surrounding table-driven runner loop body
	IfVarSourceTestScope             // Defined within the surrounding test function
	IfVarSourcePackage               // Defined as a global variable in this package, imported from another package, or defined in a helper function
)

func (vs IfVarSource) String() string {
	switch vs {
	case IfVarSourceScenario:
		return "scenario"
	case IfVarSourceIfScope:
		return "if scope"
	case IfVarSourceLoopScope:
		return "loop scope"
	case IfVarSourceTestScope:
		return "test scope"
	case IfVarSourcePackage:
		return "package"
	default:
		return "unknown"
	}
}

func (vs IfVarSource) MarshalJSON() ([]byte, error) {
	return json.Marshal(vs.String())
}

func (vs *IfVarSource) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}
	switch str {
	case "scenario":
		*vs = IfVarSourceScenario
	case "if scope":
		*vs = IfVarSourceIfScope
	case "loop scope":
		*vs = IfVarSourceLoopScope
	case "test scope":
		*vs = IfVarSourceTestScope
	case "package":
		*vs = IfVarSourcePackage
	default:
		*vs = IfVarSourceUnknown
	}
	return nil
}

// ================================================================================================

// IfVarUsage represents the way a variable or field is used in a conditional's header (initialization statement and condition check).
type IfVarUsage int

const (
	IfVarUsageNone             IfVarUsage = iota
	IfVarUsageDirectCheck                 // Direct boolean check,    e.g. `if tt.ok` or `if !localBool`
	IfVarUsageComparison                  // Binary comparison check,    e.g. `if tt.val == 5`
	IfVarUsageMethodReceiver              // Method call receiver,    e.g. `if tt.field.IsValid()`
	IfVarUsageFunctionCall                // Local function call,    e.g. `if tt.setup()` or `if localFunc()`
	IfVarUsageFunctionArg                 // Function call argument,    e.g. `if helper(tt.val)`
	IfVarUsageComputation                 // Involved in a computation,    e.g. `if val/2 > 10`
	IfVarUsageVariableCreation            // Variable declaration/assignment in init statement, e.g. `if got := ...`
	IfVarUsageOther                       // Other type of check
)

func (ict IfVarUsage) String() string {
	switch ict {
	case IfVarUsageDirectCheck:
		return "direct check"
	case IfVarUsageComparison:
		return "comparison"
	case IfVarUsageMethodReceiver:
		return "method receiver"
	case IfVarUsageFunctionCall:
		return "function call"
	case IfVarUsageFunctionArg:
		return "function argument"
	case IfVarUsageComputation:
		return "computation"
	case IfVarUsageVariableCreation:
		return "variable creation"
	case IfVarUsageOther:
		return "other"
	default:
		return "none"
	}
}

func (ict IfVarUsage) MarshalJSON() ([]byte, error) {
	return json.Marshal(ict.String())
}

func (ict *IfVarUsage) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}
	switch str {
	case "direct check":
		*ict = IfVarUsageDirectCheck
	case "comparison":
		*ict = IfVarUsageComparison
	case "method receiver":
		*ict = IfVarUsageMethodReceiver
	case "function call":
		*ict = IfVarUsageFunctionCall
	case "function argument":
		*ict = IfVarUsageFunctionArg
	case "computation":
		*ict = IfVarUsageComputation
	case "variable creation":
		*ict = IfVarUsageVariableCreation
	case "other":
		*ict = IfVarUsageOther
	default:
		*ict = IfVarUsageNone
	}
	return nil
}
