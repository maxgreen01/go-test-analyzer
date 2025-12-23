package parsercommands

import (
	"github.com/maxgreen01/go-test-analyzer/internal/config"
	"github.com/maxgreen01/go-test-analyzer/pkg/parser"

	"github.com/jessevdk/go-flags"
)

// Represents an application command that can be executed as a task by the parser.
// This is a combination of both the Parser's Task interface and the Flags package's Commander interface.
// Stores input flags for the task, as well as fields representing the data to be collected.
//
// Implementations of this interface should include fields for global and and command-specific options,
// as well as fields for any data that needs to be collected during the execution of the task.
// Implementations should use the `init` function to register themselves via the command registry.
type ParserCommand interface {
	parser.Task
	flags.Commander
}

// Stores anonymous functions used to register each command with the flag parser, which are all called by the `main` function.
// Should only be modified by calling `RegisterCommand` in the `init` function of each command implementation.
var CommandRegistry []func(*flags.Parser, *config.GlobalOptions)

// Prepares a command for use by saving the function that registers it in the flag parser.
func RegisterCommand(registerFunc func(*flags.Parser, *config.GlobalOptions)) {
	CommandRegistry = append(CommandRegistry, registerFunc)
}
