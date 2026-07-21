package features

import (
	"encoding/json"
	"go/ast"
	"go/types"
	"slices"
	"strings"

	"github.com/maxgreen01/go-test-analyzer/pkg/asttools"
)

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
// TODO SOON make this unexported once all the remaining methods can be separated from TestCase and brought into this package
func (af *analysisFeature) Register(present bool, isDelegated bool) {
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

	// TODO SOON make these unexported once all the remaining methods can be separated from TestCase and brought into this package
	Scope         *types.Scope `json:"-"` // The scope corresponding to this block, used for finding external mutations
	EnclosingFunc ast.Node     `json:"-"` // The innermost AST function enclosing this block, used for determining whether statements are inside local function literals
}

// NewFeatureSet creates a new FeatureSet without any features initialized.
func NewFeatureSet(scope *types.Scope, enclosingFunc ast.Node, delegated bool) FeatureSet {
	return FeatureSet{
		Delegated:     delegated,
		Scope:         scope,
		EnclosingFunc: enclosingFunc,
	}
}

// Bubbles up any detected features from a child to its parent, and updates the parent's delegated status accordingly.
// This modifies the parent in-place, and should be called from bottom up on the FeatureSet tree to propagate correctly.
func BubbleUp(parent, child *FeatureSet) {
	// Bubble up any `Present` features, inheriting the delegated status of the entire child or the individual feature
	parent.HasSubtest.Register(child.HasSubtest.Present, child.Delegated || child.HasSubtest.Delegated)
	parent.HasAssertion.Register(child.HasAssertion.Present, child.Delegated || child.HasAssertion.Delegated)

	// Only bubble up HasEarlyExit if the parent and child are within the same innermost enclosing function,
	// since an early exit inside a helper function doesn't exit the parent's function
	if parent.EnclosingFunc == child.EnclosingFunc {
		parent.HasEarlyExit.Register(child.HasEarlyExit.Present, child.Delegated || child.HasEarlyExit.Delegated)
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

// Maps standard `testing.TB` methods to their respective test phases.
func CategorizeStandardTestingMethod(name string) TestPhase {
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
func IsLikelyAssertion(function *types.Func) bool {
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
func ContainsTestHarness(signature *types.Signature) bool {
	// Check if the function is a method of a test harness
	if recv := signature.Recv(); recv != nil {
		if CheckForTestHarnessType(recv.Type(), 0) {
			return true
		}
	}
	// Check if the function takes a test harness as a parameter
	for param := range signature.Params().Variables() {
		if CheckForTestHarnessType(param.Type(), 0) {
			return true
		}
	}
	return false
}

const maxTestHarnessRecursionDepth = 5

// Recursively checks if a type acts as a testing harness or contains a testing harness as a struct field,
// based on structural rules rather than relying on package names.
func CheckForTestHarnessType(t types.Type, depth int) bool {
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
			if CheckForTestHarnessType(field.Type(), depth+1) {
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
