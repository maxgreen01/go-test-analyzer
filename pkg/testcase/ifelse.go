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
// This primarily serves as a container of IfClause elements, which store more detailed information about the individual branches of the if/else chain.
type IfStmt struct {
	Content     string      `json:"content"`     // The DST code of the full statement, converted to a string
	TotalLength int         `json:"totalLength"` // Total number of statements in this if/else chain, including inside nested statements
	Clauses     []*IfClause `json:"clauses"`     // List of all branches in the statement
	stmt        *dst.IfStmt `json:"-"`           // Underlying DST node
}

// Compile-time interface check
var _ ControlFlowStatement = (*IfStmt)(nil)

// Creates an IfStmt instance representing an entire if/else chain, and analyzes the clause's inner statements in a depth-first traversal
// to detect nested control flow statements and metadata features.
func CreateIfStmt(stmt *dst.IfStmt, cfa *controlFlowAnalyzer) *IfStmt {
	ifStmt := &IfStmt{
		Content: asttools.NodeToString(stmt),
		stmt:    stmt,
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

	// Propagate table-based conditional status to any clauses below a table-based clause in the same chain
	foundTableBasedClause := false
	for _, clause := range ifStmt.Clauses {
		if clause.IsTableBased {
			foundTableBasedClause = true
			continue
		}
		if foundTableBasedClause {
			clause.IsTableBased = true
		}
	}

	// Populate computed fields based on other analysis results
	ifStmt.TotalLength = TotalLength(ifStmt, cfa.countHelperLength)

	return ifStmt
}

// GetDstStmt returns the underlying DST statement representing this control flow statement.
func (ifs *IfStmt) GetDstStmt() dst.Stmt {
	return ifs.stmt
}

// GetNestedStmts returns the nested control flow statements within this statement.
// Note: Even though IfClause implements ControlFlowStatement, this intentionally flattens and aggregates clauses to avoid double-counting metrics during traversal.
func (ifs *IfStmt) GetNestedStmts() []ControlFlowStatement {
	var nested []ControlFlowStatement
	for _, clause := range ifs.Clauses {
		nested = append(nested, clause.GetNestedStmts()...)
	}
	return nested
}

// GetFeatureSets returns the metadata feature sets associated with this control flow statement.
// Note: Even though IfClause implements ControlFlowStatement, this intentionally flattens and aggregates clauses to avoid double-counting metrics during traversal.
func (ifs *IfStmt) GetFeatureSets() []features.FeatureSet {
	var featureSets []features.FeatureSet
	for _, clause := range ifs.Clauses {
		featureSets = append(featureSets, clause.GetFeatureSets()...)
	}
	return featureSets
}

// GetLength returns the number of DST statements inside this control flow statement, NOT including nested statements.
// Note: Even though IfClause implements ControlFlowStatement, this intentionally flattens and aggregates clauses to avoid double-counting metrics during traversal.
func (ifs *IfStmt) GetLength() int {
	// Excluding nested statements, the length of an if/else chain is just the sum of the lengths of its clauses
	length := 0
	for _, clause := range ifs.Clauses {
		length += clause.GetLength()
	}
	return length
}

// GetEnclosingFunction returns the outermost AST function enclosing this control flow statement.
// All clauses are expected to be in the same function, so just return the enclosing function of the first clause.
func (ifs *IfStmt) GetEnclosingFunction() ast.Node {
	if len(ifs.Clauses) == 0 {
		return nil
	}
	return ifs.Clauses[0].GetEnclosingFunction()
}

// ================================================================================================

// IfClause represents one branch of a if/else chain detected in a table-driven test.
type IfClause struct {
	Type                IfClauseType           `json:"type"`                // Type of this clause with respect to the if/else chain (then, else if, else)
	Condition           string                 `json:"condition,omitempty"` // String representation of the condition expression, if any
	Variables           []*IfVarBehavior       `json:"variables,omitempty"` // Variables and fields used in the condition, if any
	IsTableBased        bool                   `json:"isTableBased"`        // Whether the clause represents table-based conditional logic
	features.FeatureSet                        // Detected metadata features, embedded because it saves an unnecessary layer of nesting
	NestedStatements    []ControlFlowStatement `json:"nestedStatements,omitempty"` // Additional control flow statements that are contained within this branch, if any

	stmt dst.Stmt `json:"-"` // Underlying DST node
}

// Compile-time interface check
var _ ControlFlowStatement = (*IfClause)(nil)

// Creates an IfClause instance representing a single branch of an if/else chain, and analyzes the clause's inner statements in a depth-first
// traversal to detect nested control flow statements and metadata features.
func CreateIfClause(clauseStmt dst.Stmt, clauseType IfClauseType, cfa *controlFlowAnalyzer) *IfClause {
	enclosingFuncs, _ := cfa.tc.GetEnclosingFunctions(clauseStmt)
	clause := &IfClause{
		Type:       clauseType,
		FeatureSet: features.NewFeatureSet(cfa.tc.GetNodeScope(clauseStmt), enclosingFuncs, !cfa.tc.IsWithinTestFunction(clauseStmt)),
		stmt:       clauseStmt,
	}

	// Search for nested statements in the clause, and process extra fields for a "then" or "else if" clause
	// compared to an "else" clause
	switch stmt := clauseStmt.(type) {
	case *dst.IfStmt:
		clause.BodyBlock = cfa.tc.DstToAst(stmt.Body).(*ast.BlockStmt)
		// Analyze variables in the condition expression
		clause.Condition = asttools.NodeToString(stmt.Cond)
		if stmt.Init != nil {
			clause.Condition = asttools.NodeToString(stmt.Init) + "; " + clause.Condition
		}
		clause.Variables, clause.IsTableBased = analyzeCondition(stmt, cfa)

		// Manually search for nested statements
		clause.NestedStatements = cfa.analyzeNested([]dst.Node{stmt.Init, stmt.Cond, stmt.Body}, &clause.FeatureSet)

	case *dst.BlockStmt:
		clause.BodyBlock = cfa.tc.DstToAst(stmt).(*ast.BlockStmt)
		// Entire "else" clause statement is just the body
		clause.NestedStatements = cfa.analyzeNested([]dst.Node{stmt}, &clause.FeatureSet)
	}

	// Bubble up detected metadata features from nested control flow statements
	BubbleUpFeatures(&clause.FeatureSet, clause.NestedStatements)

	// Populate computed fields based on other analysis results
	clause.TotalLength = TotalLength(clause, cfa.countHelperLength)

	return clause
}

// GetDstStmt returns the underlying DST statement representing this control flow statement.
func (c *IfClause) GetDstStmt() dst.Stmt {
	return c.stmt
}

// GetNestedStmts returns the nested control flow statements within this statement.
func (c *IfClause) GetNestedStmts() []ControlFlowStatement {
	return c.NestedStatements
}

// GetFeatureSets returns the metadata feature sets associated with this control flow statement.
func (c *IfClause) GetFeatureSets() []features.FeatureSet {
	return []features.FeatureSet{c.FeatureSet}
}

// GetLength returns the number of DST statements inside this control flow statement, NOT including nested statements.
func (c *IfClause) GetLength() int {
	return c.Length
}

// GetEnclosingFunction returns the outermost AST function enclosing this control flow statement.
func (c *IfClause) GetEnclosingFunction() ast.Node {
	return c.OuterEnclosingFunc
}

// IfVarBehavior stores behavior details and source scope for a variable in a condition.
type IfVarBehavior struct {
	Name   string       `json:"name"`             // Name of the variable, e.g., `tc.Value`, `localVal[i]`, `err`
	Source IfVarSource  `json:"source"`           // Classification of the location where the variable is defined
	Usages []IfVarUsage `json:"usages,omitempty"` // Classifications of the ways the variable is used in the condition
}

// Analyzes an if clause's condition (including the initializer) to extract any variables that are used and classify their behavior.
// Also checks whether the condition represents table-based logic based on the variables it involves.
func analyzeCondition(stmt *dst.IfStmt, cfa *controlFlowAnalyzer) ([]*IfVarBehavior, bool) {
	var behaviors []*IfVarBehavior

	// Analyze the initializer statement to detect its variables (both RHS and LHS), even though the RHS is the only part that should influence the table-based status
	initBehaviors, initTableBased := analyzeConditionPart(stmt.Init, cfa, stmt, nil)
	behaviors = append(behaviors, initBehaviors...)

	// Do a second fine-grained pass to track of the table-based status of each assigned variable
	// todo CLEANUP - string map key isn't ideal, but the alternative is a slice of `dst.Expr` that we search using `dstequal` which is just annoying. This is because each actual `ident` is unique
	initVarStatuses := make(map[string]tableBasedStatus)
	switch init := stmt.Init.(type) {
	case *dst.AssignStmt:
		if len(init.Lhs) == len(init.Rhs) {
			// According to the Go spec, each RHS must be single-valued, so each corresponding LHS is independent -- check table-based status for each RHS individually
			for i, rhs := range init.Rhs {
				_, rhsTableBased := analyzeConditionPart(rhs, cfa, stmt, nil)
				initVarStatuses[asttools.NodeToString(init.Lhs[i])] = rhsTableBased
			}
		} else {
			// According to the Go spec, the only other possible RHS must be a single multi-valued expression, so all the LHS variables are set by it
			for _, lhs := range init.Lhs {
				initVarStatuses[asttools.NodeToString(lhs)] = initTableBased
			}
		}
	}

	// Split the condition by logical operators for the table-based check, but analyze all of them and merge their results
	isTableBased := false
	for _, part := range splitByLogicalOp(stmt.Cond) {
		partBehaviors, partTableBased := analyzeConditionPart(part, cfa, stmt, initVarStatuses)
		// Merge results from this part into the overall results
		behaviors = mergeVarBehaviors(behaviors, partBehaviors...)
		isTableBased = isTableBased || partTableBased == tableBasedStatusTableBased // The condition is table-based if ANY of its parts are table-based
	}

	return behaviors, isTableBased
}

// tableBasedStatus describes the table-based status of an intermediate expression related to an if/else clause's condition.
type tableBasedStatus int

const (
	tableBasedStatusDisallowed tableBasedStatus = iota // the expression cannot be table-based because it contains disallowed elements
	tableBasedStatusNeutral                            // the expression does not contain any table-based variables, but does not contain any disallowed elements either
	tableBasedStatusTableBased                         // the expression is table-based because it involves the necessary variable(s) and does not contain any disallowed elements
)

// Analyzes a single part of an if clause's condition to extract any variables that are used and classify their behavior.
// Also returns the table-based status of this condition part based on the variables it involves. `initVarStatuses` stores the table-based
// statuses of any variables on the LHS of the if clause's `Init` statement, or is `nil` if the `Init` statement itself is being analyzed.
func analyzeConditionPart(conditionPart dst.Node, cfa *controlFlowAnalyzer, ifStmt *dst.IfStmt, initVarStatuses map[string]tableBasedStatus) ([]*IfVarBehavior, tableBasedStatus) {
	var behaviors []*IfVarBehavior
	hasTableVar := false
	hasNonTableBasedVar := false

	dst.Inspect(conditionPart, func(node dst.Node) bool {
		if node == nil {
			return false
		}

		// The conditional can't be table-based if it involves any function calls other than built-in functions or scenario fields
		if call, ok := node.(*dst.CallExpr); ok {
			isAllowedFunc := false
			if cfa.scenarioSet != nil && cfa.scenarioSet.IsScenarioField(call.Fun) {
				isAllowedFunc = true
			} else if rootIdent := asttools.GetRootIdent(call.Fun, nil); rootIdent != nil {
				if obj := cfa.tc.ObjectOf(rootIdent); obj != nil {
					if _, ok := obj.(*types.Builtin); ok {
						isAllowedFunc = true
					}
				}
			}
			if !isAllowedFunc {
				hasNonTableBasedVar = true
			}
		}

		// Search for variables in the condition
		var targetIdent *dst.Ident
		switch x := node.(type) {
		case *dst.Ident:
			targetIdent = x
		case *dst.SelectorExpr:
			targetIdent = x.Sel
		default:
			return true
		}

		// Only analyze variables, parameters, results, struct fields, or constants (filtering out builtins)
		var target dst.Expr
		isTargetConst := false
		targetObj := cfa.tc.ObjectOf(targetIdent)
		if targetObj != nil && targetObj.Pkg() != nil {
			switch typ := targetObj.(type) {
			case *types.Var, *types.Const:
				if _, ok := typ.(*types.Const); ok {
					isTargetConst = true
				}
				target = node.(dst.Expr)
			}
		}
		if target == nil {
			return true // No applicable variable to analyze, so move on
		}
		if ident, ok := target.(*dst.Ident); ok && ident.Name == "_" {
			return true // Ignore blank identifier
		}

		// Create a new variable behavior struct for this variable
		name := asttools.NodeToString(target)
		source := classifyVarSource(target, cfa.tc, ifStmt, cfa.scenarioSet)
		behavior := &IfVarBehavior{Name: name, Source: source}

		// Classify the way this specific variable instance is used in either the `Init` or `Cond` of the if statement
		usage := classifyVarUsage(target, conditionPart)
		if usage != IfVarUsageNone {
			behavior.Usages = append(behavior.Usages, usage)
		}

		// Save the variable behavior if it's not already present in the result list
		behaviors = mergeVarBehaviors(behaviors, behavior)

		// Account for this variable in tracking whether the conditional is table-based.
		// Always allow constant values, since they're effectively equivalent to literals.
		switch behavior.Source {
		case IfVarSourceScenario:
			// Conditional can't be table-based without a scenario var
			hasTableVar = true
		case IfVarSourceIfScope:
			// If the variable was assigned in this conditional's `Init` statement, then its impact on table-based status depends entirely
			// on the corresponding value it was set to. Note that this includes existing variables that were reassigned even if they have
			// sources that are usually disallowed, because we know for sure what they're being set to in the initializer.
			if initVarStatuses == nil || usage == IfVarUsageVariableAssignment {
				// We're currently analyzing the initializer statement, so this shouldn't be reachable
				break
			}
			switch initVarStatuses[name] {
			case tableBasedStatusTableBased:
				hasTableVar = true
			case tableBasedStatusNeutral:
				// No effect on table-based status
			default:
				// Disallowed, including variables not found in the map (which should never happen)
				hasNonTableBasedVar = true
			}
		case IfVarSourcePackageConst:
			// Allowed unconditionally, no effect on table-based status
		case IfVarSourceLoopScope:
			// The variables defined in the runner loop are special cases that are allowed and can be considered table-based
			// (even if they don't necessarily represent scenario fields)
			if cfa.scenarioSet != nil && targetObj != nil && (targetObj == cfa.scenarioSet.runnerIndexVar || targetObj == cfa.scenarioSet.runnerValueVar) {
				hasTableVar = true
			} else {
				// Allowed if constant, disallowed otherwise
				hasNonTableBasedVar = !isTargetConst
			}
		default:
			// Allowed if constant, disallowed otherwise
			hasNonTableBasedVar = !isTargetConst
		}

		// Continue searching for other variables
		if _, ok := target.(*dst.SelectorExpr); ok {
			// We analyzed the selector as a unit already, so don't separately analyze its children too
			return false
		}
		return true
	})

	// Make the final determination of whether the conditional is table-based
	tableBased := tableBasedStatusNeutral
	if hasTableVar && !hasNonTableBasedVar {
		tableBased = tableBasedStatusTableBased
	} else if hasNonTableBasedVar {
		tableBased = tableBasedStatusDisallowed
	}
	return behaviors, tableBased
}

// Recursively splits a logical expression across all consecutive top-level `&&` and `||` logical operators.
// Stops splitting a branch of the expression when it encounters any other expression type (ignoring parentheses).
func splitByLogicalOp(expr dst.Expr) []dst.Expr {
	if expr == nil {
		return nil
	}
	if bin, ok := asttools.Unparen(expr).(*dst.BinaryExpr); ok {
		if bin.Op == token.LAND || bin.Op == token.LOR {
			return append(splitByLogicalOp(bin.X), splitByLogicalOp(bin.Y)...)
		}
	}
	return []dst.Expr{expr}
}

// Merges variable behaviors from `newBehaviors` into `existingBehaviors`, and returns the modified slice.
// When a new behavior corresponds to a variable not already present in `existingBehaviors`, it is added to `existingBehaviors`.
// Otherwise, adds the new variable's `Usages` to the corresponding behavior in `existingBehaviors`, ignoring duplicate values.
func mergeVarBehaviors(existingBehaviors []*IfVarBehavior, newBehaviors ...*IfVarBehavior) []*IfVarBehavior {
	for _, newBehavior := range newBehaviors {
		// Add the new behavior directly if it corresponds to a variable that isn't tracked already
		existingIdx := slices.IndexFunc(existingBehaviors, func(existing *IfVarBehavior) bool { return existing.Name == newBehavior.Name })
		if existingIdx < 0 {
			existingBehaviors = append(existingBehaviors, newBehavior)
			continue
		}

		// Add the new usages to an existing behavior
		existing := existingBehaviors[existingIdx]
		for _, newUsage := range newBehavior.Usages {
			if !slices.Contains(existing.Usages, newUsage) {
				existing.Usages = append(existing.Usages, newUsage)
			}
		}
	}
	return existingBehaviors
}

// Determines the scope where a given variable was defined.
func classifyVarSource(expr dst.Expr, tc *TestCase, ifStmt *dst.IfStmt, scenarioSet *ScenarioSet) IfVarSource {
	if expr == nil || tc == nil {
		return IfVarSourceUnknown
	}

	// Check if this expression represents the scenario or one of its fields
	if scenarioSet.IsScenarioField(expr) {
		return IfVarSourceScenario
	}

	// todo cleanup - could maybe return the root expression as the variable behavior struct's `name` field to strip operators
	// Completely unwrap the expression of to get the "base" identifier, which might be the scenario object itself
	rootIdent := asttools.GetRootIdent(expr, nil)
	if rootIdent == nil {
		return IfVarSourceUnknown
	}

	// Find the definition of the variable
	obj := tc.ObjectOf(rootIdent)
	if obj == nil {
		return IfVarSourceUnknown
	}

	ifScope := tc.GetNodeScope(ifStmt)
	if ifScope != nil && obj.Parent() == ifScope {
		return IfVarSourceIfScope
	}

	if scenarioSet != nil {
		runnerLoopScope := tc.GetNodeScope(scenarioSet.Runner)
		if runnerLoopScope != nil && asttools.IsScopeAncestor(runnerLoopScope, obj.Parent()) {
			return IfVarSourceLoopScope
		}
	}

	// Use the definition's enclosing functions, not those of the original if statement
	objEnclosingFuncs, _ := asttools.GetEnclosingFunctions(obj.Pos(), tc.GetPackageFiles())
	if len(objEnclosingFuncs) > 0 {
		innerEnclosingObjFunc := objEnclosingFuncs[0]
		// If the definition's closest enclosing function is the original test function, it's test scope instead of helper scope
		if innerEnclosingObjFunc != tc.DstToAst(tc.funcDecl) {
			return IfVarSourceHelperFunc
		}
	}

	testFuncScope := tc.GetFunctionScope()
	if testFuncScope != nil && asttools.IsScopeAncestor(testFuncScope, obj.Parent()) {
		return IfVarSourceTestScope
	}

	if _, isConst := obj.(*types.Const); isConst {
		return IfVarSourcePackageConst
	}
	return IfVarSourcePackageVar
}

// Determines how a variable or field expression is used inside a conditional's header.
func classifyVarUsage(target dst.Expr, conditionPart dst.Node) IfVarUsage {
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
				unwrapped = unary.X // strip address-of operator (but not any other unary operator)
			} else {
				break
			}
		}
		return unwrapped == target
	}

	// Check for `if target` outside the main inspection to avoid potential edge cases where the
	// target is nested inside an expression that didn't match the expected patterns below.
	if isTarget(conditionPart) {
		return IfVarUsageDirectCheck
	}

	result := IfVarUsageOther
	dst.Inspect(conditionPart, func(node dst.Node) bool {
		if node == nil || result != IfVarUsageOther {
			// Inspect the condition part, with short-circuiting once a result is found
			return false
		}

		switch n := node.(type) {
		case *dst.AssignStmt:
			for _, lhs := range n.Lhs {
				if isTarget(lhs) {
					result = IfVarUsageVariableAssignment
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
				// NOTE: when a condition is split by top-level logical operators, those operators are removed from the resulting parts.
				// When these parts are passed to this function, top-level variables that would otherwise be handled here (i.e. if calling
				// this function on the original condition) will instead be handled above in the `if isTarget(conditionPart)` check, which
				// still classifies them as direct checks. However, this case is still used to handle nested logical operators that were
				// not removed while splitting by logical operators.
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
		case *dst.TypeAssertExpr:
			if isTarget(n.X) {
				result = IfVarUsageTypeAssert
				return false
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
	IfVarSourceUnknown      IfVarSource = iota
	IfVarSourceScenario                 // Part of the table-driven scenario object,   e.g. `tt.field` or `tt` itself
	IfVarSourceIfScope                  // Defined within the initialization statement of the if/else clause being analyzed,   e.g. in `if val := helper(); val > 0`
	IfVarSourceLoopScope                // Defined within the surrounding table-driven runner loop body
	IfVarSourceHelperFunc               // Defined within a helper function or local closure
	IfVarSourceTestScope                // Defined within the surrounding test function
	IfVarSourcePackageConst             // Defined as a global CONSTANT in this package or imported from another package
	IfVarSourcePackageVar               // Defined as a global VARIABLE in this package or imported from another package
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
	case IfVarSourceHelperFunc:
		return "helper function"
	case IfVarSourcePackageConst:
		return "package const"
	case IfVarSourcePackageVar:
		return "package var"
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
	case "helper function":
		*vs = IfVarSourceHelperFunc
	case "package const":
		*vs = IfVarSourcePackageConst
	case "package var":
		*vs = IfVarSourcePackageVar
	default:
		*vs = IfVarSourceUnknown
	}
	return nil
}

// ================================================================================================

// IfVarUsage represents the way a variable or field is used in a conditional's header (initialization statement and condition check).
type IfVarUsage int

const (
	IfVarUsageNone               IfVarUsage = iota
	IfVarUsageDirectCheck                   // Direct boolean check,    e.g. `if tt.ok` or `if !localBool`
	IfVarUsageComparison                    // Binary comparison check,    e.g. `if tt.val == 5`
	IfVarUsageMethodReceiver                // Method call receiver,    e.g. `if tt.field.IsValid()`
	IfVarUsageFunctionCall                  // Local function call,    e.g. `if tt.setup()` or `if localFunc()`
	IfVarUsageFunctionArg                   // Function call argument,    e.g. `if helper(tt.val)`
	IfVarUsageComputation                   // Involved in a computation,    e.g. `if val/2 > 10`
	IfVarUsageTypeAssert                    // Involved in a type assertion,    e.g. `if val.(string) == "hello"`
	IfVarUsageVariableAssignment            // Variable assignment in init statement, e.g. `if got := ...`
	IfVarUsageOther                         // Other type of check
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
	case IfVarUsageTypeAssert:
		return "type assertion"
	case IfVarUsageVariableAssignment:
		return "variable assignment"
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
	case "type assertion":
		*ict = IfVarUsageTypeAssert
	case "variable assignment":
		*ict = IfVarUsageVariableAssignment
	case "other":
		*ict = IfVarUsageOther
	default:
		*ict = IfVarUsageNone
	}
	return nil
}
