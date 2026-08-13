// complexity_test.go contains table-driven tests containing various combinations of features that indicate undesired complexity.
package sampleproj

import (
	"strings"
	"testing"
)

// TestTableBasedConditionals has multiple table-based conditionals and relevant edge cases inside its runner loop.
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
		// ! NOTE: known limitation - not supported
		if tests[i].input > 0 { // index-based scenario field access
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

func anotherHelper(t *testing.T) {
	if false {
		t.Fatalf("bad helper called")
	}
}

// TestConditionalLogic includes instances of complex logic inside table-based conditionals.
func TestConditionalLogic(t *testing.T) {
	tests := []scenario{
		{name: "test1", input: 1, want: 2},
	}
	for _, tc := range tests {
		// Outer subtest should NOT be penalized
		t.Run(tc.name, func(t *testing.T) {
			if tc.input > 0 {
				// Subtest in table-based conditional should be penalized
				t.Run("outer table-based", func(t *testing.T) {
					// Subtest contained within another subtest should not be counted at all
					t.Run("contained in subtest", func(t *testing.T) {})
					if tc.want < 0 {
						for range tc.want {
							// Subtest contained within another subtest should not be counted at all, even if it's inside multiple nested control flow statements
							t.Run("contained in subtest in nested conditional", func(t *testing.T) {})
						}
					}
					// Subtest (inside helper function) contained within another subtest should not be counted at all
					runScenariosSubtest(t, tests)

					if tc.want > 0 {
						// Subtest in table-based conditional, which is contained within another subtest, should be counted toward its parent but not penalized
						t.Run("inner table-based contained in subtest", func(t *testing.T) {
							if false {
								t.Errorf("Some error") // assertion depth 3
							} else {
								if true {
									anotherHelper(t) // delegated assertions don't count toward assertion depth
								}
							}
						})
					}
				})

				if packageVar == 0 {
					// Subtest in a conditional with a table-based ancestor should be penalized
					t.Run("conditional table-based ancestor", func(t *testing.T) {})
				}
				for range -1 {
					// Subtest in a loop with a table-based ancestor should be penalized
					t.Run("loop table-based ancestor", func(t *testing.T) {
						t.Errorf("Another error") // assertion depth 1
					})
				}
			}

			if packageVar < 0 {
				// Subtest in non-table-based conditional should NOT be penalized
				t.Run("non-table-based", func(t *testing.T) {
					if false {
						t.Errorf("Yet another error: %d", tc.want) // assertion depth 2
					}
				})
			}
		})
	}
}
