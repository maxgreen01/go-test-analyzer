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

func helperConditional(tc *scenario) int {
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
	if strings.Contains(tc.name, strconv.Itoa(tc.input)) {
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
