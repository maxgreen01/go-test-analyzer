package testcase

import (
	"go/types"

	"github.com/dave/dst"
	"github.com/maxgreen01/go-test-analyzer/pkg/features"
)

// ControlFlowStatement represents a generic control flow statement (e.g. a loop or if/else chain) detected inside a test case.
type ControlFlowStatement interface {
	// GetNestedStmts returns the nested control flow statements within this statement, if any.
	GetNestedStmts() []ControlFlowStatement

	// GetFeatureSets returns the metadata feature sets associated with this control flow statement, if any.
	GetFeatureSets() []features.FeatureSet

	// GetLength returns the number of DST statements (of any kind) inside this control flow statement, NOT including nested statements.
	GetLength() int
}

// Detects and analyzes all control flow statements (without restricting to a specific type) inside the scenario runner loop of
// a table-driven test case, including statements found in expanded function calls.
func AnalyzeControlFlow(tc *TestCase, scenarioSet *ScenarioSet, parsedStmts []*ExpandedStatement) []ControlFlowStatement {
	cfs := analyzeRunnerControlFlow(tc, scenarioSet, parsedStmts, false, true)
	if cfs == nil {
		return []ControlFlowStatement{}
	}
	return cfs
}

// Performs the control flow analysis on the scenario runner loop of a table-driven test case.
// Returns the results of the analysis, or `nil` if the analysis could not be performed.
func analyzeRunnerControlFlow(tc *TestCase, scenarioSet *ScenarioSet, parsedStmts []*ExpandedStatement, analyzeConditionals bool, analyzeControlFlow bool) []ControlFlowStatement {
	if scenarioSet == nil || scenarioSet.Runner == nil {
		// Not table-driven
		return nil
	}

	// Analyze each ExpandedStatement inside the scenario runner loop body.
	// We unfortunately can't use `scenarioSet.GetRunnerStatements()` here because individual statements might not have
	// their own corresponding ExpandedStatement nodes (e.g. an IfStmt itself).
	// todo CLEANUP this would be made slightly easier if ExpandedStatements were stored in the ScenarioSet directly
	runnerExpanded := findExpandedStmt(scenarioSet.Runner, parsedStmts)
	if runnerExpanded == nil {
		return nil
	}

	cfa := &controlFlowAnalyzer{
		tc:                  tc,
		scenarioType:        scenarioSet.ScenarioType,
		loopScope:           tc.GetNodeScope(scenarioSet.Runner),
		analyzeConditionals: analyzeConditionals,
		analyzeControlFlow:  analyzeControlFlow,
	}
	seen := make(map[dst.Node]bool)

	return cfa.analyze(runnerExpanded, nil, seen)
}

// filterTyped returns a new slice containing only the ControlFlowStatements that can be asserted to a given concrete type,
// with all elements being cast to that type.
func filterTyped[T ControlFlowStatement](statements []ControlFlowStatement) []T {
	var result []T
	for _, stmt := range statements {
		if typed, ok := stmt.(T); ok {
			result = append(result, typed)
		}
	}
	return result
}

// BubbleUpFeatures bubbles up detected metadata features from nested control flow statements' feature sets to the provided parent.
func BubbleUpFeatures(parentFeatures *features.FeatureSet, nested []ControlFlowStatement) {
	// Bubble up features and delegated status from nested statements
	for _, child := range nested {
		for _, childFS := range child.GetFeatureSets() {
			features.BubbleUp(parentFeatures, childFS)
		}
	}
}

// TotalLength returns the total number of statements in the given control flow statement, including inside nested statements.
func TotalLength(cfs ControlFlowStatement) int {
	total := cfs.GetLength()
	for _, nested := range cfs.GetNestedStmts() {
		total += TotalLength(nested)
	}
	return total
}

// MaxDepth returns the maximum depth of nested control flow statements inside the given statement.
// Returns 0 if there are no nested statements, and increments by 1 for each level of nesting.
func MaxDepth(cfs ControlFlowStatement) int {
	maxDepth := 0
	for _, nested := range cfs.GetNestedStmts() {
		maxDepth = max(maxDepth, 1+MaxDepth(nested))
	}
	return maxDepth
}

// controlFlowAnalyzer encapsulates the settings and other relevant information needed to analyze control flow statements.
type controlFlowAnalyzer struct {
	tc                  *TestCase    // The test case under analysis
	scenarioType        types.Type   // Type of the scenario struct in the table-driven test
	loopScope           *types.Scope // The lexical scope of the runner loop
	analyzeLoops        bool         // Whether to analyze loops
	analyzeConditionals bool         // Whether to analyze conditionals
	analyzeControlFlow  bool         // Whether to analyze the unified control flow (no filtering by type)
}

// Recursively inspects a statement's DST structure (including expanded function calls) and type information to detect control flow
// statements (including nested statements) and metadata features. Only saves control flow statements with types corresponding to the
// specified controlFlowAnalyzer configuration. Requires a pre-built ExpandedStatement tree to expand function calls and avoid cycles.
// Returns a slice of all detected top-level (non-nested) control flow statements, with nested statements stored within their parents.
func (cfa *controlFlowAnalyzer) analyze(expanded *ExpandedStatement, parentFeatures *features.FeatureSet, seen map[dst.Node]bool) []ControlFlowStatement {
	var results []ControlFlowStatement

	// Walk the expanded statement tree and inspect each non-nil node within each statement.
	WalkExpanded(expanded, func(node dst.Node, children []*ExpandedStatement) bool {
		switch n := node.(type) {
		case *dst.RangeStmt, *dst.ForStmt:
			if !(cfa.analyzeLoops || cfa.analyzeControlFlow) {
				return true // Pass through this node without saving it, but search its children
			}
			if seen[n] {
				return false // Skip already-analyzed node entirely
			}

			// Analyze the loop and detect any children using recursion
			loop := CreateLoop(n, children, cfa)
			results = append(results, loop)
			seen[n] = true

			// Stop inspecting descendants because the body and header have already been processed recursively
			return false

		case *dst.IfStmt:
			if !(cfa.analyzeConditionals || cfa.analyzeControlFlow) {
				return true // Pass through this node without saving it, but search its children
			}
			if seen[n] {
				return false // Skip already-analyzed node entirely
			}

			// Analyze the conditional and detect any children using recursion
			ifStmt := CreateIfStmt(n, cfa, children)
			results = append(results, ifStmt)
			seen[n] = true

			// Stop inspecting descendants because the clauses have already been processed recursively
			return false

		default:
			// Check any other non-nil nodes for metadata features
			if parentFeatures != nil {
				RegisterNode(parentFeatures, node, cfa.tc)
			}
		}

		// Continue descending to search for additional control flow statements
		return true
	})

	return results
}

// Inspects a list of statement or expression nodes to detect control flow statements and metadata features, which is especially useful
// for detecting statements that are nested inside another control flow statement. Returns all detected control flow statements as a
// a single consolidated slice, and registers metadata features to the provided parent.
//
// Automatically wraps each node in a dummy ExpandedStatement using the provided children slice, and performs each analysis using a
// fresh `seen` map to avoid saving duplicate statements within a single parent while allowing repeats among different parents.
func (cfa *controlFlowAnalyzer) analyzeNested(nodes []dst.Node, expandedChildren []*ExpandedStatement, parentFeatures *features.FeatureSet) []ControlFlowStatement {
	var results []ControlFlowStatement
	for _, node := range nodes {
		var stmt dst.Stmt
		switch node := node.(type) {
		case dst.Stmt:
			stmt = node
		case dst.Expr:
			stmt = &dst.ExprStmt{X: node}
		default:
			// Skip unexpected node types
			continue
		}

		nodeResults := cfa.analyze(&ExpandedStatement{Stmt: stmt, Children: expandedChildren}, parentFeatures, make(map[dst.Node]bool))
		results = append(results, nodeResults...)
	}
	return results
}
