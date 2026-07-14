// negative_test.go contains tests that are NOT table-driven.
package sampleproj

import "testing"

// TestNoLoops is a simple test with no loops at all.
func TestNoLoops(t *testing.T) {
	got := 1 + 1
	if got != 2 {
		t.Error("math is broken")
	}
}

// TestNoLoopsSubtests is a simple test with no loops, but that could be converted to a table-driven test.
func TestNoLoopsSubtests(t *testing.T) {
	t.Run("subtest1", func(t *testing.T) {
		got := 1 + 1
		if got != 2 {
			t.Error("math is broken")
		}
	})
	t.Run("subtest2", func(t *testing.T) {
		got := 2 + 2
		if got != 4 {
			t.Error("math is broken")
		}
	})
}

// TestSetupLoopOnly uses a loop purely for setup, not as a scenario runner.
func TestSetupLoopOnly(t *testing.T) {
	var items []int
	for i := 0; i < 5; i++ {
		items = append(items, i)
	}
	if len(items) != 5 {
		t.Errorf("got %d items, want 5", len(items))
	}
}

// TestAssertionLoopOnly uses a loop purely for assertions, not as a scenario runner.
func TestAssertionLoopOnly(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}
	for i := 0; i < len(items)-1; i++ {
		if items[i] > items[i+1] {
			t.Errorf("not sorted: %d > %d", items[i], items[i+1])
		}
	}
}

// TestMutatedContainerIndexed mutates the scenario container inside the body of an indexed loop.
func TestMutatedContainerIndexed(t *testing.T) {
	items := []struct {
		number int
	}{
		{number: -1},
	}
	for i := 0; i < len(items); i++ {
		if len(items) < 5 {
			items = append(items, struct{ number int }{number: i})
		}
	}
}

// TestMutatedContainerRange mutates the scenario container inside the body of a range loop.
func TestMutatedContainerRange(t *testing.T) {
	items := []struct {
		number int
	}{
		{number: -1},
	}
	for i := range items {
		if len(items) < 5 {
			items = append(items, struct{ number int }{number: i})
		}
	}
}

// TestMutatedElement mutates the struct element inside the body of an indexed loop.
func TestMutatedElement(t *testing.T) {
	items := []struct {
		number int
	}{
		{number: 1},
	}
	for i := 0; i < len(items); i++ {
		items[i].number++
	}
}

// TestUnusedScenarios loops over the length of a struct slice, but never actually reads its data inside the loop.
func TestUnusedScenarios(t *testing.T) {
	items := []struct {
		number int
	}{
		{number: 1},
	}
	for i := 0; i < len(items); i++ {
		t.Log("hello")
	}
}

// TestZeroScenarios declares an empty scenario slice (zero tests) without using subtests.
func TestZeroScenarios(t *testing.T) {
	tests := []scenario{}
	for _, tc := range tests {
		if got := tc.input * 2; got != tc.want {
			t.Errorf("got %d, want %d", got, tc.want)
		}
	}
}

// TestZeroFields declares scenarios using an empty struct with no fields.
// ! NOTE: known limitation - this is falsely detected as table-driven.
func TestZeroFields(t *testing.T) {
	tests := []struct{}{
		{},
	}
	for _, tc := range tests {
		_ = tc
	}
}
