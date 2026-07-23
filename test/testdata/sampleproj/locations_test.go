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

// TestScenariosSameFile uses a package-level scenario variable defined in this file, WITHOUT subtests.
func TestScenariosSameFile(t *testing.T) {
	for _, tc := range sameFileScenarios {
		if got := tc.input * 2; got != tc.expected {
			t.Errorf("got %d, want %d", got, tc.expected)
		}
	}
}

// TestScenariosSameFileSubtest uses a package-level scenario variable defined in this file, WITH subtests.
func TestScenariosSameFileSubtest(t *testing.T) {
	for _, tc := range sameFileScenarios {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.input * 2; got != tc.expected {
				t.Errorf("got %d, want %d", got, tc.expected)
			}
		})
	}
}

// TestScenariosOtherFile uses a package-level scenario variable defined in a different file within the same package, WITHOUT subtests.
// ! NOTE: known limitation - this is not detected as table-driven.
func TestScenariosOtherFile(t *testing.T) {
	for _, tc := range otherFileScenarios {
		if got := tc.input * 2; got != tc.want {
			t.Errorf("got %d, want %d", got, tc.want)
		}
	}
}

// TestScenariosOtherFileSubtest uses a package-level scenario variable defined in a different file within the same package, WITH subtests.
func TestScenariosOtherFileSubtest(t *testing.T) {
	for _, tc := range otherFileScenarios {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.input * 2; got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// TestScenariosOtherPkg uses a package-level scenario variable defined in a different package, WITHOUT subtests.
// ! NOTE: known limitation - this is not detected as table-driven.
func TestScenariosOtherPkg(t *testing.T) {
	for _, tc := range testhelper.ScenariosExternal {
		if got := tc.Input * 2; got != tc.Want {
			t.Errorf("got %d, want %d", got, tc.Want)
		}
	}
}

// TestScenariosOtherPkgSubtest uses a package-level scenario variable defined in a different package, WITH subtests.
func TestScenariosOtherPkgSubtest(t *testing.T) {
	for _, tc := range testhelper.ScenariosExternal {
		t.Run(tc.Name, func(t *testing.T) {
			if got := tc.Input * 2; got != tc.Want {
				t.Errorf("got %d, want %d", got, tc.Want)
			}
		})
	}
}
