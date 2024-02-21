package harnessio

import (
	"encoding/json"
	"io"
	"os"

	"github.com/go-errors/errors"
)

// reads the json-encoded input into the [][]string, or errors
func ReadInputCommands(f *os.File) ([][]string, error) {
	bytes, err := io.ReadAll(f)
	if err != nil {
		return nil, errors.Errorf("reading stdin: %w", err)
	}

	var commandArgs [][]string
	err = json.Unmarshal(bytes, &commandArgs)
	if err != nil {
		return nil, errors.Errorf("unmarshalling stdin json to [][]string: %w", err)
	}

	numCommands := len(commandArgs)
	if numCommands == 0 {
		return nil, errors.Errorf("json args must not be empty. Requires at least one command")
	}

	for i, args := range commandArgs {
		if len(args) == 0 {
			return nil, errors.Errorf("command %v json args must not be empty.", i)
		}
	}

	return commandArgs, nil
}
