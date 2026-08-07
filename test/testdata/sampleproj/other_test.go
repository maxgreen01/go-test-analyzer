// other_test.go contains miscellaneous test cases that don't conveniently fit into the other categories.
package sampleproj

import (
	"strconv"
	"strings"
	"testing"

	"github.com/maxgreen01/go-test-analyzer/test/testdata/sampleproj/testhelper"
)

var otherFileScenarios = []scenario{
	{name: "otherFile", input: 1, want: 2},
}

// TestMultipleLoops has multiple loops, one of which is a scenario runner.
// ! NOTE: known limitation - this is not detected as table-driven because it investigates the wrong loop.
func TestMultipleLoops(t *testing.T) {
	data := make(map[int]int)
	for i := 0; i < 5; i++ {
		data[i] = i * 10
	}

	tests := []struct {
		name      string
		val       int
		wantFound bool
	}{
		{name: "good", val: 1, wantFound: true},
		{name: "bad", val: 99, wantFound: false},
	}
	for _, tc := range tests {
		if _, found := data[tc.val]; found != tc.wantFound {
			t.Errorf("got %v, want %v", found, tc.wantFound)
		}
	}

	for key := range data {
		delete(data, key)
	}
}

// TestMultipleLoops has multiple table-driven loops.
func TestMultipleTdLoops(t *testing.T) {
	positiveCases := []scenario{
		{name: "positive", input: 1, want: 2},
	}
	for _, tc := range positiveCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.input * 2; got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}

	negativeCases := []scenario{
		{name: "negative", input: -1, want: -2},
	}
	for _, tc := range negativeCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.input * 2; got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func helperLoop() {
	for i := 0; i < 5; i++ {
		if true {
			_ = i
		}
	}
}

// TestExpandedStatements calls multiple local, external, and production functions to verify their expansion behavior.
func TestExpandedStatements(t *testing.T) {
	tests := []scenario{
		{name: "positive", input: 2, want: 4},
	}
	localFunc := func(tc scenario) {
		// Production function should NOT be expanded
		t.Run(tc.name, func(t *testing.T) {
			if got := Double(tc.input); got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
		// External function should NOT be expanded
		testhelper.AssertScenarioExternal(t, testhelper.Scenario{Name: tc.name, Input: tc.input, Want: tc.want})
	}
	for _, tc := range tests {
		// Local function literal should be expanded
		localFunc(tc)
		// Same-package function should be expanded
		helperLoop()
		// Repeated control flow statements with the same parent should not be analyzed twice
		helperLoop()
	}
	// Functions called in the loop header should be expanded
	startFunc := func() int { return 0 }
	condFunc := func(i int) bool {
		for range -1 {
		}
		return i < 5
	}
	for i := startFunc(); condFunc(i); i += func() int { return 1 }() {
	}
	// Same-package function with nested calls should be expanded recursively
	runScenarios(t, tests)
}

const packageConst = 42

var packageVar = 100

func helperConditional(tc *scenario) int {
	trueVar := true
	if tc.input < 0 {
		if tc.want < 0 {
			return 1
		} else {
			// do nothing
		}
	} else if len(testhelper.MakeScenariosExternal()) > 0 {
		return 2
	} else {
	}
	if strings.Contains(tc.name, strconv.Itoa(tc.input)) || (tc.input == packageConst+packageVar && func(b bool) bool { return b }(trueVar && tc.name != "")) {
		return 3
	}
	return 0
}

// TestConditionals has multiple complex if/else chains and nested conditionals both inside and outside the runner loop.
func TestConditionals(t *testing.T) {
	flag := false
	threshold := 10
	if threshold < 0 {
		t.Fatalf("unexpected negative threshold: %d", threshold)
	} else if threshold == 0 {
		flag = true
		t.Log("zero threshold")
	}
	tests := []scenario{
		{name: "small", input: 4, want: 8},
		{name: "medium", input: 6, want: 12},
	}
	for _, tc := range tests {
		if tc.input < 0 {
			t.Fatalf("input should not be negative: %d", tc.input)
		} else if flag {
			return // skip remaining tests
		} else if tc.input < threshold/2 {
			continue // skip small inputs
		} else {
			if got := Double(tc.input); got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		}

		if tc.input%2 == 0 {
			if got := Double(tc.input); got < tc.want {
				t.Errorf("got %d, want at least %d", got, tc.want)
			}
		} else if helperConditional(&tc) == 1 {
			flag = false
		}
		helperConditional(&tc)
	}
}

// TestTableBasedConditionals has multiple table-based conditionals and relevant edge cases inside the runner loop.
func TestTableBasedConditionals(t *testing.T) {
	testVar := 20
	const testConst = 21
	localFunc := func(_ int) bool { return false }
	localStruct := struct{ act func(_ int) bool }{act: localFunc}

	type testScenario struct {
		name       string
		input      int
		want       int
		shouldErr  bool
		errorMsg   string
		check      func() bool
		anyData    any
		mapData    map[string]int
		structData struct{ x int }
	}
	tests := []testScenario{
		{name: "case1", check: func() bool { return false }},
		{name: "case2", check: func() bool { return true }},
	}
	for i, tc := range tests {

		// ======= isolated table-based conditionals =======

		if tc.shouldErr { // field direct usage
		}
		if !tc.shouldErr { // field direct usage (with negation)
		}
		if tc.check != nil { // field comparison against built-in
		}
		if tc.errorMsg != "" { // field comparison against literal
		}
		if tc.input == packageConst { // field comparison against package constant
		}
		if tc.input != testConst { // field comparison against local constant
		}
		if tc.check() { // field function call
		}
		if len(tc.name) == 0 { // built-in function with field argument
		}
		if tc.structData.x > 0 { // field struct member access
		}
		if (&tc.input) != &(*(&tc.want)) { // field usage with pointer operations or parentheses
		}
		if i%2 == 0 { // loop index variable usage
		}
		if tests[i].input > 0 { // index-based scenario field access // ! NOTE: known limitation - not supported
		}

		if _, ok := tc.anyData.(string); ok { // field type assertion
		}
		if val, ok := tc.mapData["key"]; ok && val > 0 { // field map access
		}
		if testVar == 10 && tc.shouldErr { // AND with table-based part
		}
		if testVar == 10 || tc.shouldErr { // OR with table-based part
		}
		// field usage in own initializer, with at least one table-based part
		if input, other := tc.input+1, 0; input < other {
		}
		// regular variable in own initializer, AND with at least one table-based part
		if cond := testVar > 0; cond && tc.shouldErr {
		}
		// AND/OR with table-based parts, and at least one table-based part
		if testVar == 20 && ((testConst == 20 && localFunc(tc.want)) || tc.input == 10) {
		}
		// variables in own initializer, plus AND/OR with at least one table-based part
		if one, val := 1, tc.input; one > 0 && (testVar == 20 || (tc.shouldErr && val < 0)) {
		}

		// ======= non-table-based conditionals =======

		if testVar == 10 { // regular variable usage alone
		}
		if testConst == 10 { // constant usage alone
		}
		if tc.input > testVar { // field comparison against regular variable
		}
		if localFunc(tc.input) { // user-defined function with field argument
		}
		if localStruct.act(tc.input) { // function field call from non-scenario struct
		}
		if strings.Contains(tc.name, "x") { // library function
		}
		// regular variable only in own initializer
		if bad := testVar * 2; bad > tc.input {
		}
		// AND/OR with only non-table-based parts
		if testVar == 20 && ((testConst == 20 && localFunc(tc.want)) || tc.input == testVar) {
		}

		// use of variables from disallowed sources
		for _, loopVar := range []bool{true, false} {
			if outerIfVar := !loopVar || true; outerIfVar {
				if outerIfVar || loopVar && tc.input > testVar || tc.input > packageVar {
				}
			}
		}

		// ======= table-based clause propagation & nesting =======

		if testVar == 40 { // not table-based
		} else { // not table-based
		}

		if tc.input == 0 { // table-based
		} else { // table-based because in a chain below a table-based clause
		}

		if testVar == 30 { // not table-based
		} else if packageConst == 10 { // not table-based
		} else if tc.shouldErr { // table-based
		} else if testConst < 0 { // not table-based on its own, but table-based because in a chain below a table-based clause
		} else { // not table-based on its own, but table-based because in a chain below a table-based clause
		}

		if testVar == 30 { // not table-based
			if tc.check != nil { // table-based
				if testConst < 0 { // not table-based, but counted toward the length of its table-based parent
					if tc.input > 0 { // table-based
					}
				}
			}
			if testConst < 0 { // not table-based
			}
		}
	}
}
