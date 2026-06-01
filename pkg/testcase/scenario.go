package testcase

import (
	"github.com/dave/dst"
)

// Represents an individual scenario defined by a table-driven test
// todo LATER not implemented yet
type Scenario struct {
	Name string // the detected name of the scenario, either from a "name" or "desc" field, or the key of a map

	Expr dst.Expr

	// todo maybe something like ScenarioDataStructure to type assert `Expr` more easily
	// todo maybe store the Type of this Expr
}
