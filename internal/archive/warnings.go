package archive

import "fmt"

const maxImportWarnings = 100

func limitImportWarnings(warnings []string) []string {
	if len(warnings) <= maxImportWarnings {
		return warnings
	}
	out := append([]string(nil), warnings[:maxImportWarnings]...)
	out = append(out, fmt.Sprintf("truncated %d additional warnings", len(warnings)-maxImportWarnings))
	return out
}
