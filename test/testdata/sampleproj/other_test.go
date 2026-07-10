// other_test.go contains miscellaneous test cases that don't conveniently fit into the other categories.
package sampleproj

import (
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
		_ = i
	}
}

// TestExpandedStatements calls multiple local, external, and production functions to verify their expansion behavior.
// ! NOTE: known limitation - the nested subtest is not detected.
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
	}
	// Same-package function with nested calls should be expanded recursively
	runScenarios(t, tests)
}
