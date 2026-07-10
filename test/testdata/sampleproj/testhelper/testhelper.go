// Package testhelper contains external helper utilities for tests in the sampleproj package.
package testhelper

import "testing"

type Scenario struct {
	Name  string
	Input int
	Want  int
}

var ScenariosExternal = []Scenario{
	{Name: "fromHelperPkg", Input: 1, Want: 2},
	{Name: "anotherScenario", Input: 3, Want: 6},
}

func MakeScenariosExternal() []Scenario {
	return []Scenario{
		{Name: "fromHelperPkg", Input: 1, Want: 2},
	}
}

func AssertScenarioExternal(t *testing.T, tc Scenario) {
	t.Helper()
	if got := tc.Input * 2; got != tc.Want {
		t.Errorf("got %d, want %d", got, tc.Want)
	}
}

func RunScenariosExternal(t *testing.T, tests []Scenario) {
	t.Helper()
	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			AssertScenarioExternal(t, tc)
		})
	}
}

func RunTestExternal(t *testing.T) {
	t.Helper()
	RunScenariosExternal(t, MakeScenariosExternal())
}
