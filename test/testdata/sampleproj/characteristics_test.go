// characteristics_test.go contains table-driven tests that exercise different ScenarioSet properties:
// name field variants, expected fields, function fields, and subtest usage.
package sampleproj

import "testing"

// TestNameField uses the scenario name field "scenarioName".
func TestNameField(t *testing.T) {
	tests := []struct {
		scenarioName string
		input        int
	}{
		{scenarioName: "case1", input: 1},
	}
	for _, tc := range tests {
		t.Run(tc.scenarioName, func(t *testing.T) {
			_ = tc.input
		})
	}
}

// TestSubtestNameField uses a non-standard scenario name field that is used as the scenario name.
func TestSubtestNameField(t *testing.T) {
	tests := []struct {
		identifier string
		input      int
	}{
		{identifier: "case1", input: 1},
	}
	for _, tc := range tests {
		t.Run(tc.identifier, func(t *testing.T) {
			_ = tc.input
		})
	}
}

// TestMultipleExpected has several scenario fields matching the naming convention for expected results.
func TestMultipleExpected(t *testing.T) {
	tests := []struct {
		name         string
		input        int
		expectedVal  int
		result       string
		expectStatus bool
	}{
		{name: "case1", input: 1, expectedVal: 2, result: "ok", expectStatus: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_ = tc
		})
	}
}

// TestFunctionFields has a scenario field with a function type.
func TestFunctionFields(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		validate func(int) bool
	}{
		{name: "case1", input: 1, validate: func(v int) bool { return v > 0 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.validate(tc.input) {
				t.Errorf("validation failed for %s", tc.name)
			}
		})
	}
}

// TestNoSubtest executes scenarios without using subtests.
func TestNoSubtest(t *testing.T) {
	tests := []struct {
		name  string
		input int
		want  int
	}{
		{name: "case1", input: 1, want: 2},
	}
	for _, tc := range tests {
		if tc.input*2 != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, tc.input*2, tc.want)
		}
	}
}
