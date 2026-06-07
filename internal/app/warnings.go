package app

import "fmt"

const maxReportWarnings = 100

func limitReportWarnings(warnings []string) []string {
	if len(warnings) <= maxReportWarnings {
		return warnings
	}
	out := append([]string(nil), warnings[:maxReportWarnings]...)
	out = append(out, fmt.Sprintf("truncated %d additional warnings", len(warnings)-maxReportWarnings))
	return out
}
