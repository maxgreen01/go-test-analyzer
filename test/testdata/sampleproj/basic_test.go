// basic_test.go contains common table-driven test patterns covering the supported scenario data structures
// (slice, map, or array of structs) and loop iteration styles (range, range-int, for-index).
package sampleproj

import (
	"fmt"
	"testing"
)

type scenario struct {
	name  string
	input int
	want  int
}

// TestSliceRange is a typical table-driven test: slice of structs, range loop, subtests.
func TestSliceRange(t *testing.T) {
	tests := []scenario{
		{name: "positive", input: 2, want: 4},
		{name: "negative", input: -1, want: -2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.input * 2; got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// TestArrayRange uses a fixed-size array instead of a slice.
func TestArrayRange(t *testing.T) {
	tests := [2]scenario{
		{name: "positive", input: 2, want: 4},
		{name: "negative", input: -1, want: -2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.input * 2; got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// TestMapRange uses a map keyed by test name. The struct value has no name field because the map key serves as the scenario name.
func TestMapRange(t *testing.T) {
	tests := map[string]struct {
		input int
		want  int
	}{
		"positive": {input: 2, want: 4},
		"negative": {input: -1, want: -2},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := tc.input * 2; got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// TestMapRangeNonStruct uses a map of inputs to expected outputs, without a struct.
func TestMapRangeNonStruct(t *testing.T) {
	tests := map[int]bool{
		2:  true,
		-1: false,
	}
	for input, expected := range tests {
		got := input > 0
		if got != expected {
			t.Errorf("got %t, want %t", got, expected)
		}
	}
}

// TestInlineScenarios declares the scenario slice directly in the range clause.
func TestInlineScenarios(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input int
		want  int
	}{
		{name: "positive", input: 2, want: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.input * 2; got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// TestRangeInt uses a range-over-int loop.
func TestRangeInt(t *testing.T) {
	tests := []scenario{
		{name: "positive", input: 2, want: 4},
	}
	for i := range len(tests) {
		t.Run(tests[i].name, func(t *testing.T) {
			if got := tests[i].input * 2; got != tests[i].want {
				t.Errorf("got %d, want %d", got, tests[i].want)
			}
		})
	}
}

// TestForIndexed uses a three-clause for loop with len().
func TestForIndexed(t *testing.T) {
	tests := []scenario{
		{name: "positive", input: 2, want: 4},
	}
	for i := 0; i < len(tests); i++ {
		tc := tests[i]
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.input * 2; got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// TestIndexedNoAssign uses an indexed loop without assigning the current scenario to a variable.
func TestIndexedNoAssign(t *testing.T) {
	tests := []scenario{
		{name: "positive", input: 2, want: 4},
	}
	for i := 0; i < len(tests); i++ {
		t.Run(tests[i].name, func(t *testing.T) {
			if got := tests[i].input * 2; got != tests[i].want {
				t.Errorf("got %d, want %d", got, tests[i].want)
			}
		})
	}
}

// TestRangeNonStruct uses a range statement over a non-struct slice, with subtests.
func TestRangeNonStruct(t *testing.T) {
	for _, str := range []string{"a", "b", "c"} {
		t.Run(str, func(t *testing.T) {
			if got := len(str); got != 1 {
				t.Errorf("got len %d, want len 1", got)
			}
		})
	}
}

// TestRangeOther uses a range statement over a non-categorized data structure (e.g. a channel), with subtests.
func TestRangeOther(t *testing.T) {
	ch := make(chan int, 3)
	ch <- 1
	ch <- 2
	ch <- 3
	close(ch)
	for val := range ch {
		t.Run(fmt.Sprintf("val=%d", val), func(t *testing.T) {
			if val < 0 {
				t.Errorf("got %d, want >= 0", val)
			}
		})
	}
}
