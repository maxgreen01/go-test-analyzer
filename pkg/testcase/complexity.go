package testcase

import (
	"go/ast"
	"math"
	"slices"

	"github.com/maxgreen01/go-test-analyzer/pkg/asttools"
)

// ComplexityMetrics holds the complexity metric values calculated for a table-driven test case.
type ComplexityMetrics struct {
	// NumSubtestsInConditionals is the number of subtests defined inside table-based conditionals.
	NumSubtestsInConditionals int `csv:"numSubtestsInConditionals"`
	// NumFunctionFields is the number of function fields in the scenario data type.
	NumFunctionFields int `csv:"numFunctionFields"`
	// PctRunnerStmtsInConditionals is the percentage of runner statements inside table-based conditionals, ranging from 0 to 100.
	PctRunnerStmtsInConditionals float32 `csv:"pctRunnerStmtsInConditionals"`
	// MaxAssertionDepth is the maximum depth of an assertion within conditional statements.
	MaxAssertionDepth int `csv:"maxAssertionDepth"`
	// PctTableFieldsOnlyInConditionals is the percentage of scenario fields only used inside table-based conditionals, ranging from 0 to 100.
	PctTableFieldsOnlyInConditionals float32 `csv:"pctTableFieldsOnlyInConditionals"`
}

// These constants define the weights for each complexity metric when calculating the overall severity score.
// They are each multiplied by the corresponding metric value (i.e. a linear combination) to compute the overall score.
const (
	WeightNumSubtestsInConditionals        = 10.0
	WeightNumFunctionFields                = 5.0
	WeightPctRunnerStmtsInConditionals     = 0.25 // corresponding metric is from 0-100
	WeightMaxAssertionDepth                = 5.0
	WeightPctTableFieldsOnlyInConditionals = 0.20 // corresponding metric is from 0-100
)

// OverallScore computes the overall severity score based on the metrics and their weights, rounded to one decimal place.
func (cm ComplexityMetrics) OverallScore() float32 {
	// Calculate the total severity score as a linear combination of the metrics and their weights
	overallScore := float32(cm.NumSubtestsInConditionals)*WeightNumSubtestsInConditionals +
		float32(cm.NumFunctionFields)*WeightNumFunctionFields +
		cm.PctRunnerStmtsInConditionals*WeightPctRunnerStmtsInConditionals +
		float32(cm.MaxAssertionDepth)*WeightMaxAssertionDepth +
		cm.PctTableFieldsOnlyInConditionals*WeightPctTableFieldsOnlyInConditionals

	const decimals = 1 // Number of decimal places to round to, should be positive

	ratio := math.Pow(10, decimals)
	return float32(math.Round(float64(overallScore)*ratio) / ratio)
}

// CalculateComplexity calculates the complexity metrics for this AnalysisResult.
func (ar *AnalysisResult) CalculateComplexity() ComplexityMetrics {
	metrics := ComplexityMetrics{}

	if len(ar.ControlFlowStatements) == 0 {
		// Runner loop not detected (so not table-driven), or no control flow statements to work with
		return metrics
	}

	// Implementation detail: the "root" control flow statement is always the scenario runner loop itself, and the other statements are nested inside it
	runnerLoopCfs, ok := ar.ControlFlowStatements[0].(*Loop)
	if !ok || len(ar.ControlFlowStatements) != 1 {
		// Sanity check of the above, since we'd prefer empty results instead of sneaky garbage results if the assumption is violated
		return metrics
	}

	// 1. Number of subtests in table-based conditionals
	metrics.NumSubtestsInConditionals += countTableBasedSubtests(runnerLoopCfs, ar.TestCase, false, nil, ar.ParsedStatements)

	// 2. Number of function fields in the scenario data type
	if ar.ScenarioSet != nil {
		metrics.NumFunctionFields = ar.ScenarioSet.NumFunctionFields()
	}

	// 3. Percentage of runner statements inside table-based conditionals
	totalRunnerLength := runnerLoopCfs.TotalLength
	if totalRunnerLength > 0 {
		topLevelLength := calcTotalTableBasedLength(runnerLoopCfs, runnerLoopCfs.GetEnclosingFunction())
		metrics.PctRunnerStmtsInConditionals = float32(topLevelLength) / float32(totalRunnerLength) * 100.0
	}

	// 4. Maximum depth of an assertion within conditional statements
	metrics.MaxAssertionDepth = findMaxAssertionDepth(runnerLoopCfs, runnerLoopCfs.GetEnclosingFunction(), 0)

	// 5. Percentage of table fields that are only used inside table-based conditionals
	metrics.PctTableFieldsOnlyInConditionals = calculatePctTableFieldsOnlyInConditionals(ar, runnerLoopCfs)

	return metrics
}

// =========================================== Individual Metric Calculations ===========================================

// todo cleanup - would be nice to use interface methods instead of repeated type switches

// countTableBasedSubtests recursively counts the number of table-based subtests within a given control flow statement.
// This only counts subtests within table-based conditionals, excluding those that are contained inside larger table-based subtests.
func countTableBasedSubtests(cfs ControlFlowStatement, tc *TestCase, insideTableBased bool, tableBasedParentBodies []ast.Node, parsedStmts []*ExpandedStatement) int {
	count := 0

	// Check if this CFS is inside a subtest within a table-based conditional ancestor
	insideTableBasedSubtest := false
	expanded := FindEnclosingExpandedStmt(cfs.GetDstStmt(), parsedStmts, tc)
	for _, parentBlock := range tableBasedParentBodies {
		if IsStmtInsideSubtest(expanded, parentBlock, tc) {
			insideTableBasedSubtest = true
			break
		}
	}

	switch stmt := cfs.(type) {
	case *IfStmt:
		for _, clause := range stmt.Clauses {
			// Only count subtests inside clauses with a table-based context that aren't already inside a larger table-based subtest
			clauseInsideTableBased := insideTableBased || clause.IsTableBased
			if clauseInsideTableBased && !insideTableBasedSubtest {
				count += clause.NumSubtests
			}

			// If this clause is table-based, add it to the list of table-based parents for recursion
			if clause.IsTableBased && clause.BodyBlock != nil {
				tableBasedParentBodies = append(slices.Clone(tableBasedParentBodies), clause.BodyBlock)
			}

			for _, nested := range clause.NestedStatements {
				count += countTableBasedSubtests(nested, tc, clauseInsideTableBased, tableBasedParentBodies, parsedStmts)
			}
		}
	case *Loop:
		// Only count subtests inside loops with a table-based context that aren't already inside a larger table-based subtest
		if insideTableBased && !insideTableBasedSubtest {
			count += stmt.NumSubtests
		}
		for _, nested := range stmt.NestedStatements {
			count += countTableBasedSubtests(nested, tc, insideTableBased, tableBasedParentBodies, parsedStmts)
		}
	}
	return count
}

// calcTotalTableBasedLength recursively calculates the total length of the table-based conditionals inside a given control flow statement.
// Note: we intentionally avoid recomputing TotalLength() to prevent accidentally using different parameters than when the field value (which is saved to JSON) is computed.
func calcTotalTableBasedLength(cfs ControlFlowStatement, runnerFunc ast.Node) int {
	// (Copied from `CountStatement()` -- make sure these stay in sync!)
	// Ignore statements defined outside the control flow statement's enclosing package-level function, unless `countHelperLength` is enabled
	if !countHelperLength && !asttools.IsNodeInside(cfs.GetEnclosingFunction(), runnerFunc) {
		return 0
	}
	length := 0
	switch stmt := cfs.(type) {
	case *IfStmt:
		for _, clause := range stmt.Clauses {
			// Found a table-based clause, so save its length (plus 1 for the clause itself) and don't check into its nested statements
			if clause.IsTableBased {
				length += clause.TotalLength + 1
				continue
			}
			// For non-table-based clauses, check their nested statements for table-based conditionals
			for _, nested := range clause.NestedStatements {
				length += calcTotalTableBasedLength(nested, runnerFunc)
			}
		}
	case *Loop:
		// Check nested statements for table-based conditionals
		for _, nested := range stmt.NestedStatements {
			length += calcTotalTableBasedLength(nested, runnerFunc)
		}
	}
	return length
}

// findMaxAssertionDepth recursively finds the maximum conditional depth of non-delegated assertions within the main test function.
func findMaxAssertionDepth(cfs ControlFlowStatement, runnerFunc ast.Node, currDepth int) int {
	maxDepth := 0 // the maximum assertion depth among this statement and its nested statements

	// Ignore control flow statements outside the main test function (i.e. in a helper function)
	if cfs.GetEnclosingFunction() != runnerFunc {
		return maxDepth
	}

	// Check if this statement has an assertion, which would mean the current depth is the maximum depth we've seen so far
	switch stmt := cfs.(type) {
	case *IfStmt:
		// All conditionals increment the current depth
		currDepth++
		for _, clause := range stmt.Clauses {
			if clause.HasAssertion.Present && !clause.HasAssertion.Delegated {
				maxDepth = currDepth
			}
		}
	case *Loop:
		// Loops do not increment the current depth
		if stmt.HasAssertion.Present && !stmt.HasAssertion.Delegated {
			maxDepth = currDepth
		}
	}

	// Recursively check if there are deeper assertions within the nested statements
	for _, nested := range cfs.GetNestedStmts() {
		if childMax := findMaxAssertionDepth(nested, runnerFunc, currDepth); childMax > maxDepth {
			maxDepth = childMax
		}
	}

	return maxDepth
}

// calculatePctTableFieldsOnlyInConditionals calculates the percentage of scenario fields only used inside table-based conditionals.
func calculatePctTableFieldsOnlyInConditionals(ar *AnalysisResult, runnerLoopCfs *Loop) float32 {
	if ar.ScenarioSet == nil {
		return 0.0
	}
	allFields := slices.Collect(ar.ScenarioSet.GetFields())
	if len(allFields) == 0 {
		return 0.0
	}
	tc := ar.TestCase
	typeInfo := tc.TypeInfo()
	if typeInfo == nil {
		return 0.0
	}

	// Collect the AST nodes for all table-based conditional clauses, which are the only places where table fields are allowed to be used for this metric.
	// These implicitly include the initializer, condition, and body of each clause.
	var tableBasedClauseNodes []ast.Node
	clauses := collectIfClauses([]ControlFlowStatement{runnerLoopCfs})
	for _, clause := range clauses {
		if !clause.IsTableBased {
			continue
		}
		if clauseAst := tc.DstToAst(clause.GetDstStmt()); clauseAst != nil {
			tableBasedClauseNodes = append(tableBasedClauseNodes, clauseAst)
		}
	}

	// Check one-by-one if each field is only used inside table-based conditionals
	fieldsOnlyInConditionals := 0
	for _, field := range allFields {
		// Find all usages (not including the definition) of the field variable inside the test function
		var usages []ast.Node
		ast.Inspect(runnerLoopCfs.GetEnclosingFunction(), func(n ast.Node) bool {
			// Search for SelectorExpr because searching for Ident would also include the field initializations, which we don't want
			if selector, ok := n.(*ast.SelectorExpr); ok {
				if obj := typeInfo.Uses[selector.Sel]; obj == field {
					usages = append(usages, selector)
				}
			}
			return true
		})

		if len(usages) == 0 {
			// If the field is never used, we don't count it as being "only used inside table-based conditionals"
			continue
		}

		// Make sure every usage is inside one of the table-based conditionals
		onlyInConditionals := true
		for _, node := range usages {
			insideTableClause := slices.ContainsFunc(tableBasedClauseNodes, func(clause ast.Node) bool {
				return asttools.IsNodeInside(node, clause)
			})
			if !insideTableClause {
				onlyInConditionals = false
				break
			}
		}

		if onlyInConditionals {
			fieldsOnlyInConditionals++
		}
	}

	return float32(fieldsOnlyInConditionals) / float32(len(allFields)) * 100.0
}

// collectIfClauses returns all IfClause structs contained within a list of control flow statements, including their nested statements.
func collectIfClauses(cfsList []ControlFlowStatement) []*IfClause {
	var clauses []*IfClause
	for _, cfs := range cfsList {
		switch stmt := cfs.(type) {
		case *IfStmt:
			// Specifically collect the IfClause structs
			for _, clause := range stmt.Clauses {
				clauses = append(clauses, clause)
			}
		}
		// Collect clauses from children
		clauses = append(clauses, collectIfClauses(cfs.GetNestedStmts())...)
	}
	return clauses
}
