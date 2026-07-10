// thirdparty_test.go contains tests utilizing 3rd-party packages to ensure loop detection and analysis works correctly in their presence.
package sampleproj

import (
	"testing"

	qt "github.com/frankban/quicktest"
	"github.com/stretchr/testify/assert"
)

type thirdPartyScenario struct {
	name  string
	input []int
	want  []int
}

// TestThirdPartyPackage compares slices using testify and quicktest.
func TestThirdPartyPackage(t *testing.T) {
	c := qt.New(t)
	tests := []thirdPartyScenario{
		{name: "equal", input: []int{1, 2}, want: []int{1, 2}},
	}
	for _, tc := range tests {
		// Testify assertion
		assert.Equal(t, tc.want, tc.input)
		
		// Quicktest subtest & assertion
		c.Run(tc.name, func(c *qt.C) {
			c.Assert(tc.input, qt.DeepEquals, tc.want)
		})
	}
}
