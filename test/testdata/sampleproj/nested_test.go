// nested_test.go contains table-driven tests involving nested loop structures.
package sampleproj

import "testing"

// TestNestedOuterTD has a table-driven outer loop WITHOUT subtests containing a nested regular loop.
func TestNestedOuterTD(t *testing.T) {
	tests := []struct {
		name   string
		inputs []int
		want   int
	}{
		{name: "sum positive", inputs: []int{1, 2, 3}, want: 6},
	}
	for _, tc := range tests {
		sum := 0
		for _, v := range tc.inputs {
			sum += v
		}
		if sum != tc.want {
			t.Errorf("got %d, want %d", sum, tc.want)
		}
	}
}

// TestNestedOuterSubtest has a table-driven outer loop WITH subtests containing a nested regular loop.
func TestNestedOuterSubtest(t *testing.T) {
	tests := []struct {
		name   string
		inputs []int
		want   int
	}{
		{name: "sum positive", inputs: []int{1, 2, 3}, want: 6},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sum := 0
			for _, v := range tc.inputs {
				sum += v
			}
			if sum != tc.want {
				t.Errorf("got %d, want %d", sum, tc.want)
			}
		})
	}
}

// TestNestedInnerTD has a regular outer loop containing a table-driven inner loop WITHOUT subtests.
func TestNestedInnerTD(t *testing.T) {
	tests := []scenario{
		{name: "positive", input: 1, want: 2},
	}
	for cond := range []bool{true, false} {
		for _, tc := range tests {
			if got := tc.input * 2; got != tc.want {
				t.Errorf("(cond: %v) got %d, want %d", cond, got, tc.want)
			}
		}
	}
}

// TestNestedInnerSubtest has a regular outer loop containing a table-driven inner loop WITH subtests.
func TestNestedInnerSubtest(t *testing.T) {
	tests := []scenario{
		{name: "positive", input: 1, want: 2},
	}
	for cond := range []bool{true, false} {
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				if got := tc.input * 2; got != tc.want {
					t.Errorf("(cond: %v) got %d, want %d", cond, got, tc.want)
				}
			})
		}
	}
}

// TestNestedBothTD has a table-driven outer loop WITHOUT subtests containing a table-driven inner loop WITHOUT subtests.
func TestNestedBothTD(t *testing.T) {
	tests := []struct {
		groupName string
		scenarios []scenario
	}{
		{
			groupName: "group1",
			scenarios: []scenario{
				{name: "sub1", input: 1, want: 2},
				{name: "sub2", input: 2, want: 4},
			},
		},
	}
	for _, group := range tests {
		for _, tc := range group.scenarios {
			if got := tc.input * 2; got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		}
	}
}

// TestNestedBothSubtest has a table-driven outer loop WITH subtests containing a table-driven inner loop WITH subtests,
// producing two levels of subtests.
func TestNestedBothSubtest(t *testing.T) {
	tests := []struct {
		groupName string
		scenarios []scenario
	}{
		{
			groupName: "group1",
			scenarios: []scenario{
				{name: "sub1", input: 1, want: 2},
				{name: "sub2", input: 2, want: 4},
			},
		},
	}
	for _, group := range tests {
		t.Run(group.groupName, func(t *testing.T) {
			for _, tc := range group.scenarios {
				t.Run(tc.name, func(t *testing.T) {
					if got := tc.input * 2; got != tc.want {
						t.Errorf("got %d, want %d", got, tc.want)
					}
				})
			}
		})
	}
}
