package timefmt

import "time"

const sortableUTCLayout = "2006-01-02T15:04:05.000000000Z"

func FormatUTC(t time.Time) string {
	return t.UTC().Format(sortableUTCLayout)
}
