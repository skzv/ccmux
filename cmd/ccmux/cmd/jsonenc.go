package cmd

import (
	"encoding/json"
	"os"
)

// newStdoutEncoder returns a JSON encoder that writes to stdout.
// Split out so any subcommand that wants newline-delimited JSON
// output uses the same encoder configuration.
func newStdoutEncoder() *json.Encoder {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	return enc
}
