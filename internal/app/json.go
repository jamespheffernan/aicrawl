package app

import (
	"encoding/json"
	"fmt"
	"io"
)

func writeJSON(w io.Writer, value any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}

func writeTextLine(w io.Writer, format string, args ...any) error {
	_, err := fmt.Fprintf(w, format+"\n", args...)
	return err
}
