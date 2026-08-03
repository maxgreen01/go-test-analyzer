package testcase

import (
	"go/ast"
	"go/types"
	"slices"

	"github.com/dave/dst"
	"github.com/maxgreen01/go-test-analyzer/pkg/asttools"
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

	// GetEnclosingFunction returns the outermost AST function enclosing this control flow statement.
	GetEnclosingFunction() ast.Node
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
	if scenarioSet == nil || scenarioSet.Runner == nil /* || !scenarioSet.IsTableDriven() */ { // FIXME maybe re-add the table-driven check here
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
		countHelperLength:   false, // Intentionally disabled because this makes statement length harder to work with
	}

	return cfa.analyze(runnerExpanded, make(map[dst.Node]bool))
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
// If `countHelperLength == false`, the nested control flow statements detected outside the given statement's outermost enclosing
// function will not be counted toward the total length.
func TotalLength(cfs ControlFlowStatement, countHelperLength bool) int {
	return totalLengthFiltered(cfs, cfs.GetEnclosingFunction(), countHelperLength)
}

// totalLengthFiltered recursively calculates the total length of a control flow statement, optionally ignoring nested statements
// that are outside the given parent function.
func totalLengthFiltered(cfs ControlFlowStatement, parentOuterFunc ast.Node, countHelperLength bool) int {
	total := cfs.GetLength()
	for _, nested := range cfs.GetNestedStmts() {
		if !countHelperLength && parentOuterFunc != nil && nested.GetEnclosingFunction() != parentOuterFunc {
			// Ignore statements defined outside the given parent function
			continue
		}
		total += totalLengthFiltered(nested, parentOuterFunc, countHelperLength)
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
	countHelperLength   bool         // Whether to count statements detected outside a control flow statement's enclosing package-level function toward their length

	nodeToExpanded map[dst.Node]*ExpandedStatement // Mapping of DST nodes to their corresponding ExpandedStatements, used for expanding function calls
	nodeToParent   map[dst.Node]dst.Node
}

// Detects control flow statements in the given ExpandedStatement tree based on the controlFlowAnalyzer's configuration.
// Returns a slice of all detected non-nested control flow statements, with nested statements stored within their parents.
func (cfa *controlFlowAnalyzer) analyze(expanded *ExpandedStatement, seen map[dst.Node]bool) []ControlFlowStatement {
	cfa.nodeToExpanded = BuildNodeMap(expanded)
	cfa.nodeToParent = BuildParentMap(expanded)
	return cfa.walk(expanded.Stmt, nil, seen)
}

// Recursively inspects a DST node and its children (including expanded function calls) to detect control flow statements and
// metadata features. Only saves control flow statements with types corresponding to the specified controlFlowAnalyzer configuration.
// Returns a slice of all detected top-level (non-nested) control flow statements, with nested statements stored within their parents.
//
// Expects that the controlFlowAnalyzer has already been initialized with the necessary context. Uses a map for avoiding control flow
// statements that have already been analyzed.
func (cfa *controlFlowAnalyzer) walk(root dst.Node, parentFeatures *features.FeatureSet, seen map[dst.Node]bool) []ControlFlowStatement {
	var results []ControlFlowStatement

	// Walk the expanded statement tree and inspect each non-nil node within each statement.
	WalkExpanded(root, cfa.nodeToExpanded, func(node dst.Node) bool {
		// Count the number of statements encountered
		if stmt, ok := node.(dst.Stmt); ok && parentFeatures != nil {
			cfa.CountStatement(stmt, parentFeatures)
		}

		// Actually inspect the node
		switch node := node.(type) {
		case *dst.RangeStmt, *dst.ForStmt:
			if !(cfa.analyzeLoops || cfa.analyzeControlFlow) {
				return true // Pass through this node without saving it, but search its children
			}
			if seen[node] {
				return false // Skip already-analyzed node entirely
			}
			seen[node] = true

			// Analyze the loop and detect any children using recursion
			loop := CreateLoop(node, cfa)
			results = append(results, loop)

			// Stop inspecting descendants because the body and header have already been processed recursively
			return false

		case *dst.IfStmt:
			if !(cfa.analyzeConditionals || cfa.analyzeControlFlow) {
				return true // Pass through this node without saving it, but search its children
			}
			if seen[node] {
				return false // Skip already-analyzed node entirely
			}
			seen[node] = true

			// Analyze the conditional and detect any children using recursion
			ifStmt := CreateIfStmt(node, cfa)
			results = append(results, ifStmt)

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
// Automatically performs each analysis using a fresh `seen` map to avoid saving duplicate statements within a single parent while
// allowing repeats among different parents.
func (cfa *controlFlowAnalyzer) analyzeNested(nodes []dst.Node, parentFeatures *features.FeatureSet) []ControlFlowStatement {
	var results []ControlFlowStatement
	for _, node := range nodes {
		if node == nil {
			continue
		}
		nodeResults := cfa.walk(node, parentFeatures, make(map[dst.Node]bool))
		results = append(results, nodeResults...)
	}
	return results
}

// TODO SOON / MAYBE - make this into a `FeatureSet` method once we can separate it from TestCase circular dependency
// CountStatement increments the length of the given FeatureSet if the statement matches the required conditions.
// This verifies that the statement originates from inside the control flow body, can ignore statements defined
// outside the control flow statement's function (based on the cfa configuration), and ensures the statement is
// not nested inside another control flow block.
func (cfa *controlFlowAnalyzer) CountStatement(stmt dst.Stmt, featureSet *features.FeatureSet) {
	if stmt == nil || featureSet == nil {
		return
	}

	switch s := stmt.(type) {
	case *dst.BlockStmt, *dst.CaseClause, *dst.CommClause, *dst.EmptyStmt:
		// Skip block containers from being counted directly
		return
	case *dst.ExprStmt:
		// Skip dummy ExprStmt wrappers created during expansion, which don't have corresponding AST nodes
		if cfa.tc.DstToAst(s) == nil {
			return
		}
	}

	// Make sure this statement was somehow called from inside the control flow statement's body block. If the statement is directly inside the body block,
	// then the loop will break immediately with `curr == stmt`. If the statement is inside an expanded function call, then the loop will traverse the statement's
	// parents until it finds a function call site that is located somewhere inside the control flow statement's body block. This avoids counting statements that
	// appear in the header of a control flow statement, and statements that are outside the control flow statement entirely.
	anchoredInBody := false
	for curr := dst.Node(stmt); curr != nil; curr = cfa.nodeToParent[curr] {
		if astNode := cfa.tc.DstToAst(curr); astNode != nil && asttools.IsNodeInside(astNode, featureSet.BodyBlock) {
			// The statement (or expanded function call site) was found inside the body block
			anchoredInBody = true
			break
		}
	}
	if !anchoredInBody {
		return
	}

	astNode := cfa.tc.DstToAst(stmt)
	if astNode == nil {
		return
	}
	// Ignore statements defined outside the control flow statement's enclosing package-level function, unless `countHelperLength` is enabled
	if !cfa.countHelperLength && !asttools.IsNodeInside(astNode, featureSet.OuterEnclosingFunc) {
		return
	}

	// Check that the statement is directly inside the control flow statement's body or an expanded function body,
	// and not nested inside another control flow statement.

	// Find all the statement's structural parents
	path := asttools.GetEnclosingNodes(astNode.Pos(), cfa.tc.GetPackageFiles())
	stmtIdx := slices.Index(path, astNode)
	if stmtIdx == -1 {
		return
	}

	// This access is safe because the node we searched for could never be the AST file itself, which is always the last element
	parent := path[stmtIdx+1]

	// Check if the statement is directly inside the control flow statement's body
	isDirectInBody := (parent == featureSet.BodyBlock)

	// Check if the statement is directly inside an expanded function's body, which happens when
	// its grandparent is a function, and its parent is the corresponding block statement
	isDirectInFunc := false
	if len(path) > stmtIdx+2 {
		switch fn := path[stmtIdx+2].(type) {
		case *ast.FuncDecl:
			isDirectInFunc = (fn.Body == parent)
		case *ast.FuncLit:
			isDirectInFunc = (fn.Body == parent)
		}
	}

	// Increment the length because the statement isn't nested in another control flow statement
	if isDirectInBody || isDirectInFunc {
		featureSet.Length++
	}
}
