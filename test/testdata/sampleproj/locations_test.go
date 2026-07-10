// locations_test.go contains table-driven tests where scenario data is defined in different scopes.
package sampleproj

import (
	"testing"

	"github.com/maxgreen01/go-test-analyzer/test/testdata/sampleproj/testhelper"
)

var sameFileScenarios = []struct {
	name     string
	input    int
	expected int
}{
	{name: "sameFile", input: 1, expected: 2},
}

// TestScenariosSameFile uses a package-level scenario variable defined in this file.
func TestScenariosSameFile(t *testing.T) {
	for _, tc := range sameFileScenarios {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.input * 2; got != tc.expected {
				t.Errorf("got %d, want %d", got, tc.expected)
			}
		})
	}
}

// TestScenariosOtherFile uses a package-level scenario variable defined in a different file within the same package.
// ! NOTE: known limitation - this is not detected as table-driven.
func TestScenariosOtherFile(t *testing.T) {
	for _, tc := range otherFileScenarios {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.input * 2; got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// TestScenariosOtherPkg uses a package-level scenario variable defined in a different package.
// ! NOTE: known limitation - this is not detected as table-driven.
func TestScenariosOtherPkg(t *testing.T) {
	for _, tc := range testhelper.ScenariosExternal {
		t.Run(tc.Name, func(t *testing.T) {
			if got := tc.Input * 2; got != tc.Want {
				t.Errorf("got %d, want %d", got, tc.Want)
			}
		})
	}
}
