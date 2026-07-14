// helpers_test.go contains table-driven tests where different parts of the test are extracted to helper functions within the same package.
package sampleproj

import "testing"

func makeScenarios() []scenario {
	return []scenario{
		{name: "fromFunc", input: 1, want: 2},
	}
}

func assertScenario(t *testing.T, tc scenario) {
	t.Helper()
	if got := tc.input * 2; got != tc.want {
		t.Errorf("got %d, want %d", got, tc.want)
	}
}

func runScenarios(t *testing.T, tests []scenario) {
	t.Helper()
	for _, tc := range tests {
		assertScenario(t, tc)
	}
}

func runScenariosSubtest(t *testing.T, tests []scenario) {
	t.Helper()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertScenario(t, tc)
		})
	}
}

func runTest(t *testing.T) {
	t.Helper()
	runScenarios(t, makeScenarios())
}

func runTestSubtest(t *testing.T) {
	t.Helper()
	runScenariosSubtest(t, makeScenarios())
}

// TestScenariosFromHelper defines scenarios via a same-package helper function, WITHOUT subtests.
// ! NOTE: known limitation - this is not detected as table-driven.
func TestScenariosFromHelper(t *testing.T) {
	tests := makeScenarios()
	for _, tc := range tests {
		assertScenario(t, tc)
	}
}

// TestScenariosFromHelperSubtest defines scenarios via a same-package helper function, WITH subtests.
func TestScenariosFromHelperSubtest(t *testing.T) {
	tests := makeScenarios()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertScenario(t, tc)
		})
	}
}

// TestAssertInHelper delegates the assertion to a same-package helper function, WITHOUT subtests.
func TestAssertInHelper(t *testing.T) {
	tests := []scenario{
		{name: "delegated", input: 1, want: 2},
	}
	for _, tc := range tests {
		assertScenario(t, tc)
	}
}

// TestAssertInHelperSubtest delegates the assertion to a same-package helper function, WITH subtests.
func TestAssertInHelperSubtest(t *testing.T) {
	tests := []scenario{
		{name: "delegated", input: 1, want: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertScenario(t, tc)
		})
	}
}

// TestLoopInHelper delegates the entire runner loop to a same-package helper function WITHOUT subtests.
// ! NOTE: known limitation - this is not detected as table-driven.
func TestLoopInHelper(t *testing.T) {
	tests := []scenario{
		{name: "delegated", input: 1, want: 2},
	}
	runScenarios(t, tests)
}

// TestLoopInHelperSubtest delegates the entire runner loop to a same-package helper function WITH subtests.
// ! NOTE: known limitation - this is not detected as table-driven.
func TestLoopInHelperSubtest(t *testing.T) {
	tests := []scenario{
		{name: "delegated", input: 1, want: 2},
	}
	runScenariosSubtest(t, tests)
}

// TestEverythingInHelper delegates the entire test to a same-package helper function WITHOUT subtests.
// ! NOTE: known limitation - this is not detected as table-driven.
func TestEverythingInHelper(t *testing.T) {
	runTest(t)
}

// TestEverythingInHelperSubtest delegates the entire test to a same-package helper function WITH subtests.
// ! NOTE: known limitation - this is not detected as table-driven.
func TestEverythingInHelperSubtest(t *testing.T) {
	runTestSubtest(t)
}
