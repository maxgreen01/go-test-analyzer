// external_test.go contains table-driven tests where different parts of the test are extracted to helper functions in a separate package.
package sampleproj

import (
	"testing"

	"github.com/maxgreen01/go-test-analyzer/test/testdata/sampleproj/testhelper"
)

// TestScenariosFromExternalPkg defines scenarios via an external package function WITHOUT subtests.
// ! NOTE: known limitation - this is not detected as table-driven.
func TestScenariosFromExternalPkg(t *testing.T) {
	tests := testhelper.MakeScenariosExternal()
	for _, tc := range tests {
		if got := tc.Input * 2; got != tc.Want {
			t.Errorf("got %d, want %d", got, tc.Want)
		}
	}
}

// TestScenariosFromExternalPkgSubtest defines scenarios via an external package function WITH subtests.
func TestScenariosFromExternalPkgSubtest(t *testing.T) {
	tests := testhelper.MakeScenariosExternal()
	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			if got := tc.Input * 2; got != tc.Want {
				t.Errorf("got %d, want %d", got, tc.Want)
			}
		})
	}
}

// TestAssertInExternalPkg delegates the assertion to an external package helper WITHOUT subtests.
func TestAssertInExternalPkg(t *testing.T) {
	tests := []testhelper.Scenario{
		{Name: "delegated", Input: 1, Want: 2},
	}
	for _, tc := range tests {
		testhelper.AssertScenarioExternal(t, tc)
	}
}

// TestAssertInExternalPkgSubtest delegates the assertion to an external package helper WITH subtests.
func TestAssertInExternalPkgSubtest(t *testing.T) {
	tests := []testhelper.Scenario{
		{Name: "delegated", Input: 1, Want: 2},
	}
	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			testhelper.AssertScenarioExternal(t, tc)
		})
	}
}

// TestLoopInExternalPkg delegates the entire runner loop to an external package helper WITHOUT subtests.
// ! NOTE: known limitation - this is not detected as table-driven.
func TestLoopInExternalPkg(t *testing.T) {
	tests := []testhelper.Scenario{
		{Name: "delegated", Input: 1, Want: 2},
	}
	testhelper.RunScenariosExternal(t, tests)
}

// TestLoopInExternalPkgSubtest delegates the entire runner loop to an external package helper WITH subtests.
// ! NOTE: known limitation - this is not detected as table-driven.
func TestLoopInExternalPkgSubtest(t *testing.T) {
	tests := []testhelper.Scenario{
		{Name: "delegated", Input: 1, Want: 2},
	}
	testhelper.RunScenariosSubtestExternal(t, tests)
}

// TestEverythingInExternalPkg delegates the entire test to an external package helper WITHOUT subtests.
// ! NOTE: known limitation - this is not detected as table-driven.
func TestEverythingInExternalPkg(t *testing.T) {
	testhelper.RunTestExternal(t)
}

// TestEverythingInExternalPkgSubtest delegates the entire test to an external package helper WITH subtests.
// ! NOTE: known limitation - this is not detected as table-driven.
func TestEverythingInExternalPkgSubtest(t *testing.T) {
	testhelper.RunTestSubtestExternal(t)
}
