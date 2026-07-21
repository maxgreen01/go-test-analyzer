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

// Detects and analyzes all conditionals found inside the scenario runner loop body of a table-driven test case, including those called in expanded function calls.
// todo LATER - maybe add support for detecting switch statements
func AnalyzeConditionals(tc *TestCase, scenarioSet *ScenarioSet, parsedStmts []*ExpandedStatement) *IfElseAnalysisResult {
	result := &IfElseAnalysisResult{}
	seen := make(map[dst.Node]bool)

	if scenarioSet == nil || scenarioSet.Runner == nil {
		// Not table-driven
		return result
	}
	scenarioType := scenarioSet.ScenarioType
	loopScope := tc.GetNodeScope(scenarioSet.Runner)

	// Analyze each ExpandedStatement inside the scenario runner loop body.
	// We unfortunately can't use `scenarioSet.GetRunnerStatements()` here because individual statements might not have
	// their own corresponding ExpandedStatement nodes (e.g. an if/else statement itself).
	// todo CLEANUP this would be made slightly easier if ExpandedStatements were stored in the ScenarioSet directly
	runnerExpanded := findExpandedStmt(scenarioSet.Runner, parsedStmts)
	if runnerExpanded != nil {
		ifs := analyzeStmtConditionals(runnerExpanded, nil, tc, scenarioType, loopScope, seen)
		result.Conditionals = append(result.Conditionals, ifs...)
	}

	result.NumConditionals = len(result.Conditionals)

	return result
}

// Recursively inspects a statement's DST structure (including expanded function calls) and type information to detect if/else,
// statements, including nested conditionals and metadata features. Uses pre-built ExpandedStatements to expand function calls
// and avoid cycles. Builds a hierarchy of conditionals by tracking a reference to the closest parent conditional and modifying
// its fields in-place. Avoids saving duplicate conditionals within each conditional's nested slice, but allows repeats among
// different parent conditionals. Returns a slice of all detected top-level (non-nested) conditionals if no parent is provided,
// or nil if a parent is provided.
func analyzeStmtConditionals(expanded *ExpandedStatement, parentClause *IfClause, tc *TestCase, scenarioType types.Type, loopScope *types.Scope, seen map[dst.Node]bool) []*IfStmt {
	var topLevelIfs []*IfStmt // only used if `parentClause` is nil

	// Walk the expanded statement tree and inspect each non-nil node within each statement
	WalkExpanded(expanded, func(node dst.Node, children []*ExpandedStatement) bool {
		switch n := node.(type) {
		case *dst.IfStmt:
			// Ignore conditionals that have already been analyzed directly under this parent
			if seen[n] {
				return false
			}

			// Analyze the conditional and detect any children using recursion
			ifStmt := CreateIfStmt(n, tc, scenarioType, loopScope, children, seen)

			// Save nested conditionals under their parent, or collect top-level conditionals to return
			if parentClause != nil {
				parentClause.NestedConditionals = append(parentClause.NestedConditionals, ifStmt)
			} else {
				topLevelIfs = append(topLevelIfs, ifStmt)
			}
			seen[n] = true

			// Stop inspecting descendants because the body has already been processed by `CreateIfStmt`
			return false

		default:
			// Check function calls (which have already been expanded) and any other non-nil nodes for metadata features
			if parentClause != nil {
				RegisterNode(&parentClause.FeatureSet, node, tc)
			}
		}

		// Continue descending to search for additional conditionals
		return true
	})

	return topLevelIfs
}

// ================================================================================================

// IfStmt represents an entire if/else chain detected in a table-driven test.
type IfStmt struct {
	Content     string      `json:"content"`     // The DST code of the full statement, converted to a string
	TotalLength int         `json:"totalLength"` // Total number of statements in this if/else chain, including inside nested conditionals
	InHelper    bool        `json:"inHelper"`    // True if the statement is physically located in a helper function outside the test function
	Clauses     []*IfClause `json:"clauses"`     // List of all branches in the statement
}

// Creates an IfStmt instance representing an entire if/else chain, and analyzes the clause's inner statements to detect nested conditionals
// and metadata features in a depth-first traversal.
//
// Note that `children` is expected to be a superset of the statements actually inside the clauses, since it should hold all the child
// statements of the most recently expanded function call or original statement, so it may include statements next to the if/else chain.
func CreateIfStmt(stmt *dst.IfStmt, tc *TestCase, scenarioType types.Type, loopScope *types.Scope, children []*ExpandedStatement, seen map[dst.Node]bool) *IfStmt {
	ifStmt := &IfStmt{
		Content:  asttools.NodeToString(stmt),
		InHelper: !tc.IsWithinTestFunction(stmt),
	}

	// Convert nested DST "else if" chains into a flat slice of IfClause elements, and analyze nested statements within each clause

	// Track the current branch. Expected to be an IfStmt ("then" or "else if"), BlockStmt ("else"), or nil (end of chain w/o "else").
	curr := stmt
	for curr != nil {
		// The first IfStmt clause is considered the "then" clause, subsequent ones are "else if"
		clauseType := IfClauseTypeElseIf
		if curr == stmt {
			clauseType = IfClauseTypeThen
		}

		ifClause := CreateIfClause(curr, clauseType, tc, scenarioType, loopScope, children, seen)
		ifStmt.Clauses = append(ifStmt.Clauses, ifClause)

		// Move to the next clause in the chain
		if nextIf, ok := curr.Else.(*dst.IfStmt); ok {
			// Another "else if"
			curr = nextIf

		} else if elseBlock, ok := curr.Else.(*dst.BlockStmt); ok {
			// Finish looping with the "else" clause
			elseClause := CreateIfClause(elseBlock, IfClauseTypeElse, tc, scenarioType, loopScope, children, seen)
			ifStmt.Clauses = append(ifStmt.Clauses, elseClause)
			break
		} else {
			// No more clauses in the chain, so finish looping
			break
		}
	}

	// Compute calculated fields based on finished clauses
	for _, clause := range ifStmt.Clauses {
		ifStmt.TotalLength += clause.TotalLength()
	}

	return ifStmt
}

// MaxDepth returns the maximum depth of nested conditionals in this if/else chain.
// Returns 0 if there are no nested conditionals, and increments by 1 for each level of nesting.
func (ifs *IfStmt) MaxDepth() int {
	maxDepth := 0
	for _, clause := range ifs.Clauses {
		maxDepth = max(maxDepth, clause.MaxDepth()) // don't add 1 here because the clause itself isn't a new level of nesting
	}
	return maxDepth
}

// IfClause represents one branch of a if/else chain detected in a table-driven test.
type IfClause struct {
	Type                IfClauseType     `json:"type"`                // Type of this clause with respect to the if/else chain (then, else if, else)
	Condition           string           `json:"condition,omitempty"` // String representation of the condition expression, if any
	Variables           []*IfVarBehavior `json:"variables,omitempty"` // Variables and fields used in the condition, if any
	Length              int              `json:"length"`              // Number of statements in this clause
	features.FeatureSet                  // Detected metadata features, embedded because it saves an unnecessary layer of nesting
	NestedConditionals  []*IfStmt        `json:"nestedConditionals,omitempty"` // Additional if/else statements that are contained within this branch, if any
}

// Creates an IfClause instance representing a single branch of an if/else chain, and analyzes the clause's inner statements to detect
// nested conditionals and metadata features in a depth-first traversal.
//
// Note that `children` is expected to be a superset of the statements actually inside the clause, since it should hold all the child
// statements of the most recently expanded function call or original statement, so it may include statements next to the if/else chain.
func CreateIfClause(clauseStmt dst.Stmt, clauseType IfClauseType, tc *TestCase, scenarioType types.Type, loopScope *types.Scope, children []*ExpandedStatement, seen map[dst.Node]bool) *IfClause {
	var enclosingFunc ast.Node
	if astFuncs, _ := tc.GetEnclosingFunctions(clauseStmt); len(astFuncs) > 0 {
		enclosingFunc = astFuncs[0]
	}
	clause := &IfClause{
		Type:       clauseType,
		FeatureSet: features.NewFeatureSet(tc.GetNodeScope(clauseStmt), enclosingFunc, !tc.IsWithinTestFunction(clauseStmt)),
	}

	// Handle different logic for a "then" or "else if" clause, compared to an "else" clause
	var body *dst.BlockStmt
	switch stmt := clauseStmt.(type) {
	case *dst.IfStmt:
		// Analyze variables in the condition expression
		clause.Condition = asttools.NodeToString(stmt.Cond)
		if stmt.Init != nil {
			clause.Condition = asttools.NodeToString(stmt.Init) + "; " + clause.Condition
		}
		clause.Variables = analyzeCondition(stmt, tc, scenarioType, loopScope)
		body = stmt.Body
		// Manually search for nested statements in this statement's `Init` and `Cond` fields, since they aren't checked anywhere else
		analyzeStmtConditionals(&ExpandedStatement{Stmt: stmt.Init, Children: children}, clause, tc, scenarioType, loopScope, make(map[dst.Node]bool))
		analyzeStmtConditionals(&ExpandedStatement{Stmt: &dst.ExprStmt{X: stmt.Cond}, Children: children}, clause, tc, scenarioType, loopScope, make(map[dst.Node]bool))

	case *dst.BlockStmt:
		body = stmt
	}

	// Analyze nested statements using a dummy ExpandedStatement representing the statement body, and reuse the containing statement's children.
	// Use a fresh `seen` map to avoid duplicate conditionals within this new parent, but allow them to be repeated under different parents.
	// Any detected nested conditionals or metadata features are automatically attached to this loop.
	if body != nil {
		clause.Length = len(body.List)
		analyzeStmtConditionals(&ExpandedStatement{Stmt: body, Children: children}, clause, tc, scenarioType, loopScope, make(map[dst.Node]bool))
	}

	// Make sure all metadata features are propagated and set correctly
	clause.finalize()

	return clause
}

// Bubbles up detected metadata features from nested conditionals, and populates computed fields based on other analysis results
func (clause *IfClause) finalize() {
	// Bubble up features and delegated status from nested conditionals
	for _, child := range clause.NestedConditionals {
		for _, childClause := range child.Clauses {
			features.BubbleUp(&clause.FeatureSet, &childClause.FeatureSet)
		}
	}
}

// TotalLength returns the total number of statements in this if/else clause, including inside nested conditionals.
func (clause *IfClause) TotalLength() int {
	total := clause.Length
	for _, nested := range clause.NestedConditionals {
		total += nested.TotalLength
	}
	return total
}

// MaxDepth returns the maximum depth of nested conditionals in this if/else clause.
// Returns 0 if there are no nested conditionals, and increments by 1 for each level of nesting.
func (clause *IfClause) MaxDepth() int {
	maxDepth := 0
	for _, nested := range clause.NestedConditionals {
		maxDepth = max(maxDepth, 1+nested.MaxDepth())
	}
	return maxDepth
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
