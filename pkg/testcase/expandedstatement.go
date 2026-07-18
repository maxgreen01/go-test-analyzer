package testcase

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"iter"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"github.com/dave/dst"
	"github.com/maxgreen01/go-test-analyzer/pkg/asttools"
	"golang.org/x/tools/go/ast/astutil"
)

// Represents the recursively expanded form of a DST statement as a G-tree, based on the following rules:
//
//   - At each recursive step, the statement to be expanded is stored in the `Stmt` field. This may be a
//     dummy `ExprStmt` wrapping an expression found as part of another statement.
//   - If the statement is a function call, the function's own body statements are expanded recursively and
//     stored in the `Children` field of the function call.
//   - If the statement indirectly involves function calls (e.g. as part of an assignment or conditional
//     statement), those calls are also stored in the `Children` field and similarly expanded.
//   - If the statement uses function literals as arguments to a function call, those function literals are
//     also stored in the `Children` field and similarly expanded.
//   - Any instances of indirect function calls or function literals are expanded and stored before the body
//     statements of a called function.
//   - If the statement does not involve any function calls that should be expanded, its `Children` field is
//     nil.
type ExpandedStatement struct {
	// The original statement
	Stmt dst.Stmt

	// The expanded form of the called function's inner statements, or nil if the statement is not a function call
	Children []*ExpandedStatement
}

// Recursively create the fully expanded form of a function call statement, expanding depth first.
// If `testOnly` is true, only expand statements that are defined in a file with a `_test.go` suffix.
func ExpandStatement(stmt dst.Stmt, tc *TestCase, testOnly bool) *ExpandedStatement {
	return expandStatementWithStack(stmt, tc, testOnly, nil, 0)
}

// Same as ExpandStatement, but with a maximum expansion depth limit.
// For example, if `maxDepth` is 1, functions will only be expanded if they appear directly in the original statement.
// If `maxDepth` is <= 0, there is no limit to the depth of expansion (so this is equivalent to ExpandStatement).
func ExpandStatementWithDepth(stmt dst.Stmt, tc *TestCase, testOnly bool, maxDepth int) *ExpandedStatement {
	return expandStatementWithStack(stmt, tc, testOnly, nil, maxDepth)
}

// Helper for ExpandStatement that tracks the function call stack to avoid expanding recursive calls, and supports a maximum
// depth limit (where <= 0 means no limit) based on the length of the call stack.
// Note that the order of processing a statement's "children" is partially determined by the implementation of `dst.Inspect()`.
func expandStatementWithStack(stmt dst.Stmt, tc *TestCase, testOnly bool, callStack []dst.Node, maxDepth int) *ExpandedStatement {
	if stmt == nil {
		return nil
	}
	if tc == nil {
		slog.Error("Cannot expand statement because TestCase is nil")
		return nil
	}
	fset := tc.FileSet()
	if fset == nil {
		slog.Error("Cannot expand statement because TestCase's FileSet is nil", "statement", fmt.Sprintf("%T", stmt), "testCase", tc)
		return nil
	}

	// Create the "root" ExpandedStatement for the original statement
	root := &ExpandedStatement{
		Stmt: stmt,
	}

	// Use `dst.Inspect` to walk all nodes in the statement, looking for function calls to expand.
	// Any time a function that can be expanded is found, it's treated as a new child of its parent statement.
	// This means functions called in the parameters or body of a node will be treated as separate children, which
	// will all be expanded as well. These non-body function calls are parsed and saved before a function's body statements.
	dst.Inspect(stmt, func(n dst.Node) bool {
		if n == nil {
			return false
		}

		// If we encounter a function literal as part of a larger expression, we should only expand its statements if it's the root statement being expanded.
		// There isn't any need to expand a statement like `x := func() {...}`  until the function is called, which is handled as a CallExpr.
		// If a FuncLit is specifically being expanded, it should already be wrapped in an ExprStmt as the root to act as a new layer of nesting.
		if funcLit, ok := n.(*dst.FuncLit); ok {
			if exprStmt, ok := root.Stmt.(*dst.ExprStmt); ok && exprStmt.X == funcLit {
				if funcLit.Body != nil {
					expandInnerStatements(root, funcLit, tc, testOnly, callStack, maxDepth)
				}
			}
			// Don't check the FuncLit's statements since we either shouldn't be expanding them, or they've already been expanded manually
			return false
		}

		// Look for function calls, and only proceed once we've found one
		callExpr, ok := n.(*dst.CallExpr)
		if !ok {
			// The only time we want to continue checking the node's children via `Inspect()` is if the node is NOT a function call.
			// If the node is a function call, we instead want to manually visit the arguments and function definition to expand them properly.
			return true
		}

		// Limit call tree expansion to the specified maximum depth
		if maxDepth > 0 && len(callStack) >= maxDepth {
			return true
		}

		// Now that we've found a function call, wrap it in an ExprStmt to act as a new layer of nesting before continuing.
		// Only do this step if the root is something other than the ExprStmt corresponding to this call, meaning the call is not wrapped yet.
		parent := root
		if exprStmt, ok := root.Stmt.(*dst.ExprStmt); !ok || exprStmt.X != callExpr {
			parent = &ExpandedStatement{
				Stmt: &dst.ExprStmt{X: callExpr},
			}
			// Save a reference to the new parent in the root statement so all the upcoming expansions are connected to the root
			root.Children = append(root.Children, parent)
		}

		// Expand all non-nested CallExpr and FuncLit nodes inside the function call before expanding the function body

		// First, check the function expression itself to find chained method calls (e.g. `a.B()` in `a.B().c.D()` )
		expandableNodes := findExpandableNodes(callExpr.Fun)

		// Next, check the arguments of the function call
		for _, arg := range callExpr.Args {
			expandableArgs := findExpandableNodes(arg)
			if len(expandableArgs) == 0 {
				// If the arg doesn't contain any statements to expand, check if the argument itself resolves to a function literal that can be expanded,
				// e.g. `x := func() {...};  otherFunc(x)`
				definition, err := FindDefinition(arg, tc, testOnly)
				if err == nil && definition != nil {
					if funcLit, ok := definition.Node.(*dst.FuncLit); ok {
						expandableArgs = append(expandableArgs, funcLit)
					}
				}
			}
			expandableNodes = append(expandableNodes, expandableArgs...)
		}

		// Expand the detected nodes using an unmodified callstack, since these functions are run in the same scope as the original statement
		for _, expandable := range expandableNodes {
			argStmt := &dst.ExprStmt{X: expandable}
			parent.Children = append(parent.Children, expandStatementWithStack(argStmt, tc, testOnly, callStack, maxDepth))
		}

		// Find the definition of the function being called
		definition, err := FindDefinition(callExpr.Fun, tc, testOnly)
		if err != nil {
			slog.Error("Could not find definition of function call", "err", err, "position", fset.Position(tc.DstStartPos(callExpr)), "test", tc)
			return false
		}
		if definition == nil {
			// Don't expand this statement for some non-error reason
			return false
		}

		// Expand the function's body
		expandInnerStatements(parent, definition.Node, tc, testOnly, callStack, maxDepth)

		return false
	}) // end of `dst.Inspect()`

	return root
}

// Helper for expandStatementWithStack that expands a function's inner statements with connection to a parent, checking for recursive functions.
func expandInnerStatements(parent *ExpandedStatement, node dst.Node, tc *TestCase, testOnly bool, callStack []dst.Node, maxDepth int) {
	// Avoid expanding recursive functions by checking the callstack
	if slices.Contains(callStack, node) {
		slog.Debug("Skipping expansion of recursive function call", "test", tc)
		return
	}

	var innerStmts []dst.Stmt
	switch funcDef := node.(type) {
	case *dst.FuncDecl:
		if funcDef.Body == nil {
			slog.Debug("Skipping expansion of function with nil body", "function", funcDef.Name.Name, "test", tc)
			return
		}
		innerStmts = funcDef.Body.List
	case *dst.FuncLit:
		if funcDef.Body == nil {
			slog.Debug("Skipping expansion of function literal with nil body", "test", tc)
			return
		}
		innerStmts = funcDef.Body.List

	default:
		// Function body can't be accessed normally (maybe func is declared with `var` then defined later), so don't expand it
		slog.Debug("Skipping expansion of function without obvious body", "nodeType", fmt.Sprintf("%T", funcDef), "test", tc)
		return
	}

	// Add the current function node to the callstack to indicate that we'll be working "inside" it
	newCallStack := append(slices.Clone(callStack), node)

	// Recursively expand the function's inner statements using the updated callstack
	for _, inner := range innerStmts {
		parent.Children = append(parent.Children, expandStatementWithStack(inner, tc, testOnly, newCallStack, maxDepth))
	}
}

// Helper for expandStatementWithStack that inspects an expression and returns any CallExpr and FuncLit nodes that can be expanded.
// Only returns nodes that are not nested inside any other CallExpr or FuncLit within the given expression, since those will be checked later.
func findExpandableNodes(expr dst.Expr) []dst.Expr {
	var nodes []dst.Expr
	dst.Inspect(expr, func(n dst.Node) bool {
		if n == nil {
			return false
		}
		switch n.(type) {
		case *dst.CallExpr, *dst.FuncLit:
			nodes = append(nodes, n.(dst.Expr))
			return false // Don't continue descending into this node
		}
		return true
	})
	return nodes
}

// Represents the definition of an expression as found by FindDefinition.
type ExpressionDefinition struct {
	// The DST node representing the actual expression definition
	Node dst.Node

	// The DST file that contains the definition, or nil if it was not found
	File *dst.File
}

// Memoization cache for FindDefinition to avoid redundant lookups.
// Keys are strings formatted as "<pkgInfoAddress>-<position>-<project>-<package>-<testOnly>".
// Values are pointers to ExpressionDefinition structs.
// This is the concurrent-safe version of a `map[string]*ExpressionDefinition`.
//
// Note: this cache is never cleared, so it may contain stale entries. If the same
// package is analyzed multiple times in the same program execution, this could lead
// to false cache hits referencing nodes from a previous analysis. This is avoided by
// including the memory address of the TestCase's `decorator.Package` in the key, which
// is assumed to be unique for each package, including across different analysis runs.
// todo CLEANUP - using a pointer key is a little hacky - probably better to clear the stale values when each project is finished running
var findDefinitionMemo sync.Map

// Return the DST definition and of the expression within the specified TestCase's package, if it exists.
// Also returns the DST file that contains the definition if it is successfully found, or nil in all other cases.
// If the expression is not an identifier or selector expression, returns the original expression.
// Returns nil for both return values (indicating that the definition was deliberately excluded) in the following cases:
//   - The expression is not defined in the specified context package
//   - If `testOnly` is true and the expression is not defined in a file with a `_test.go` suffix
func FindDefinition(expr dst.Expr, tc *TestCase, testOnly bool) (*ExpressionDefinition, error) {
	if tc == nil {
		return nil, fmt.Errorf("TestCase is nil")
	}

	var ident *dst.Ident
	switch x := expr.(type) {
	case *dst.Ident:
		ident = x
	case *dst.SelectorExpr:
		ident = x.Sel
	default:
		return &ExpressionDefinition{Node: expr}, nil // not an identifier or selector expression
	}

	// Don't process expressions that have been added manually (e.g. inside a helper function that has already been refactored)
	if !tc.DstStartPos(ident).IsValid() {
		slog.Debug("Ignoring identifier with invalid position", "identifier", ident.Name, "testCase", tc)
		return nil, nil
	}

	// Don't attempt to expand functions that aren't defined within the same package path as the current project.
	// This helps avoid expanding functions defined in external or built-in libraries, and universe-scope functions.
	obj, isSamePackage, err := tc.GetIdentDefinition(ident)
	if err != nil {
		return nil, err
	}
	if !isSamePackage {
		return nil, nil
	}
	pos := obj.Pos()

	// Check the memoization cache to see if the definition has already been found
	cacheKey := fmt.Sprintf("%p-%d-%s-%s-%v", tc.pkgInfo, pos, tc.ImportPath, tc.ProjectName, testOnly)
	if cached, ok := findDefinitionMemo.Load(cacheKey); ok {
		// Definition already found, so return it
		if definition, ok := cached.(*ExpressionDefinition); ok {
			return definition, nil
		}
		return nil, nil
	}

	// Find the AST file containing the object
	definitionFile := asttools.GetEnclosingFile(pos, tc.GetPackageFiles())
	if definitionFile == nil {
		return nil, fmt.Errorf("could not find definition file for identifier %q", ident.Name)
	}

	if testOnly {
		// Only expand definitions inside test files
		fset := tc.FileSet()
		if fset == nil {
			return nil, fmt.Errorf("could not check definition file path for identifier %q because FileSet is nil", ident.Name)
		}
		if !strings.HasSuffix(fset.Position(definitionFile.FileStart).Filename, "_test.go") {
			// Definition not in a test file
			slog.Debug("Ignoring identifier definition found outside a test file", "identifier", ident.Name, "test", tc)
			findDefinitionMemo.Store(cacheKey, nil) // Store the result in the memoization cache
			return nil, nil
		}
	}

	// Get the AST node corresponding to the object, plus its ancestors
	path, _ := astutil.PathEnclosingInterval(definitionFile, pos, pos)

	// Check the first element for the desired result (note that the path is never empty)
	node := path[0]

	// If the first node is a file, it means the identifier definition could not be found
	if _, ok := node.(*ast.File); ok {
		// Definition not found
		return nil, fmt.Errorf("could not find definition for identifier %q", ident.Name)
	}

	// The first node is expected to be the original identifier itself, so the second node should be the actual target definition
	if _, ok := node.(*ast.Ident); ok && len(path) > 1 && path[1] != nil {
		resolvedNode := extractFuncLitFromAssignment(tc.AstToDst(path[1]), ident.Name)
		definition := &ExpressionDefinition{Node: resolvedNode, File: tc.AstToDst(definitionFile).(*dst.File)}
		// slog.Debug("Found definition for identifier", "identifier", ident.Name, "position", tc.DstStartPos(definition.Node), "test", tc)

		findDefinitionMemo.Store(cacheKey, definition) // Store the definition in the memoization cache
		return definition, nil
	}

	return nil, fmt.Errorf("found definition for identifier %q, but found unexpected results", ident.Name)
}

// Extracts a function literal from the RHS of a variable assignment or declaration if it corresponds to the given identifier.
// For example, returns the function literal from `x := func() {...}` if `identName` is "x".
// Returns the original node if the node is not an assignment or declaration, or if no matching function literal is found.
func extractFuncLitFromAssignment(node dst.Node, identName string) dst.Node {
	switch assign := node.(type) {
	case *dst.AssignStmt:
		for i, lhs := range assign.Lhs {
			if id, ok := lhs.(*dst.Ident); ok && id.Name == identName && i < len(assign.Rhs) {
				if funcLit, ok := assign.Rhs[i].(*dst.FuncLit); ok {
					return funcLit
				}
			}
		}
	case *dst.ValueSpec:
		for i, name := range assign.Names {
			if name.Name == identName && i < len(assign.Values) {
				if funcLit, ok := assign.Values[i].(*dst.FuncLit); ok {
					return funcLit
				}
			}
		}
	}
	return node
}

//
// ========== Traversal Methods ==========
//

// Returns an iterator over all the ExpandedStatement structs contained within the ExpandedStatement in a pre-order traversal starting with the root.
func (es *ExpandedStatement) All() iter.Seq[*ExpandedStatement] {
	return func(yield func(*ExpandedStatement) bool) {
		es.push(yield)
	}
}

// Returns an iterator over all the DST statements contained within the ExpandedStatement in a pre-order traversal starting with the root.
func (es *ExpandedStatement) AllStatements() iter.Seq[dst.Stmt] {
	return func(yield func(dst.Stmt) bool) {
		for expanded := range es.All() {
			if !yield(expanded.Stmt) {
				return
			}
		}
	}
}

// Pushes all DST elements of the ExpandedStatement to the provided yield function in a pre-order manner
func (es *ExpandedStatement) push(yield func(*ExpandedStatement) bool) bool {
	if es == nil {
		return false
	}
	// Only perform the operation on the statement itself
	if !yield(es) {
		return false
	}
	for _, child := range es.Children {
		if !child.push(yield) {
			return false
		}
	}
	return true
}

// Returns the ExpandedStatement corresponding to the given statement by searching within the provided ExpandedStatements.
// Must be the exact same object (not just equivalent) to be found. Returns nil if not found.
func findExpandedStmt(stmt dst.Stmt, parsedStmts []*ExpandedStatement) *ExpandedStatement {
	for _, estmt := range parsedStmts {
		for node := range estmt.All() {
			if node.Stmt == stmt {
				return node
			}
		}
	}
	return nil
}

//
// =============== Output Methods ===============
//

// Return a string representation of an expanded statement, including the stringified versions of its children.
func (es *ExpandedStatement) String() string {
	if es == nil {
		return "ExpandedStatement{Stmt: nil}"
	}
	if es.Children == nil {
		// If there are no children, just return the stringified statement
		return fmt.Sprintf("ExpandedStatement{Stmt: %v}", asttools.NodeToString(es.Stmt))
	}

	children := make([]string, len(es.Children))
	for i, child := range es.Children {
		children[i] = child.String()
	}
	return fmt.Sprintf("ExpandedStatement{Stmt: %v, Children: [%v]}", asttools.NodeToString(es.Stmt), strings.Join(children, ", "))
}

// Helper struct for Marshaling and Unmarshaling JSON
type expandedStatementJSON struct {
	Stmt     string               `json:"stmt"`
	Children []*ExpandedStatement `json:"children,omitempty"`
}

// Marshal a TestCase for JSON output
func (es *ExpandedStatement) MarshalJSON() ([]byte, error) {
	return json.Marshal(expandedStatementJSON{
		Stmt:     asttools.NodeToString(es.Stmt),
		Children: es.Children,
	})
}

// Unmarshal a TestCase from JSON
func (es *ExpandedStatement) UnmarshalJSON(data []byte) error {
	var jsonData expandedStatementJSON
	if err := json.Unmarshal(data, &jsonData); err != nil {
		return err
	}

	var recoveredStmt dst.Stmt
	expr, err := asttools.StringToNode(jsonData.Stmt)
	if err != nil {
		slog.Error("Failed to parse ExpandedStatement from JSON", "err", err)
	} else {
		// Only check the type if the string was parsed successfully
		if stmt, ok := expr.(dst.Stmt); ok {
			recoveredStmt = stmt
		} else {
			slog.Error("Failed to parse ExpandedStatement from JSON because it is not a valid statement", "string", jsonData.Stmt)
		}
	}

	*es = ExpandedStatement{
		Stmt:     recoveredStmt,
		Children: jsonData.Children,
	}
	return nil
}

// WalkExpanded recursively visits all statements in an ExpandedStatement tree, and inspects each node within each statement.
// Follows the same ordering and expansion rules as `ExpandStatement()`: expanding call expressions, only expanding function
// literals when they're called, and avoiding recursive calls.
//
// The callback function `visit` is invoked for each non-nil node that is found, where `node` is a node found directly within
// the expanded statement, and `children` are the children of the most recent ExpandedStatement.
// If `visit` returns false, the walker will not descend into the node's children.
func WalkExpanded(expanded *ExpandedStatement, visit func(node dst.Node, children []*ExpandedStatement) bool) {
	if expanded == nil || expanded.Stmt == nil {
		return
	}
	dst.Inspect(expanded.Stmt, func(node dst.Node) bool {
		if node == nil {
			return false
		}

		// Only descend into a function literal if it's the root of the expanded statement, meaning it's the
		// subject of the current analysis. Other function literals will be analyzed separately when they are called.
		if funcLit, ok := node.(*dst.FuncLit); ok {
			if exprStmt, ok := expanded.Stmt.(*dst.ExprStmt); ok && exprStmt.X == funcLit {
				return visit(funcLit, expanded.Children)
			}
			return false
		}

		// Manually process function calls so the function's body and the CallExpr's direct children can be expanded properly
		if callExpr, ok := node.(*dst.CallExpr); ok {
			// First, visit the CallExpr itself
			if !visit(callExpr, expanded.Children) {
				return false
			}

			// Next, find and recursively walk the CallExpr's children in the expanded tree
			var targetChildren []*ExpandedStatement
			for estmt := range expanded.All() {
				// From the ExpandedStatement internals, we expect that a `CallExpr` must be wrapped in `ExprStmt`
				if exprStmt, ok := estmt.Stmt.(*dst.ExprStmt); ok && exprStmt.X == callExpr {
					targetChildren = estmt.Children
					break
				}
			}
			for _, child := range targetChildren {
				WalkExpanded(child, visit)
			}

			// Stop inspecting descendants because we already manually analyzed all the CallExpr's children
			return false
		}

		// Only continue descending if the provided callback returns true
		return visit(node, expanded.Children)
	})
}
