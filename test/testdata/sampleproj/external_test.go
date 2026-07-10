// external_test.go contains table-driven tests where different parts of the test are extracted to helper functions in a separate package.
package sampleproj

import (
	"testing"

	"github.com/maxgreen01/go-test-analyzer/test/testdata/sampleproj/testhelper"
)

// TestScenariosFromExternalPkg defines scenarios via an external package function.
// ! NOTE: known limitation - this is not detected as table-driven.
func TestScenariosFromExternalPkg(t *testing.T) {
	tests := testhelper.MakeScenariosExternal()
	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			if got := tc.Input * 2; got != tc.Want {
				t.Errorf("got %d, want %d", got, tc.Want)
			}
		})
	}
}

// TestAssertInExternalPkg delegates the assertion to an external package helper.
func TestAssertInExternalPkg(t *testing.T) {
	tests := []testhelper.Scenario{
		{Name: "delegated", Input: 1, Want: 2},
	}
	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			testhelper.AssertScenarioExternal(t, tc)
		})
	}
}

// TestLoopInExternalPkg delegates the entire runner loop to an external package helper.
// ! NOTE: known limitation - this is not detected as table-driven.
func TestLoopInExternalPkg(t *testing.T) {
	tests := []testhelper.Scenario{
		{Name: "delegated", Input: 1, Want: 2},
	}
	testhelper.RunScenariosExternal(t, tests)
}

// TestEverythingInExternalPkg delegates the entire test to an external package helper.
// ! NOTE: known limitation - this is not detected as table-driven.
func TestEverythingInExternalPkg(t *testing.T) {
	testhelper.RunTestExternal(t)
}
