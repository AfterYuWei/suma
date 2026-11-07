package taskcenter

import (
	"time"

	"github.com/robfig/cron/v3"
)

// parser accepts the standard five cron fields plus @descriptors. Seconds are
// not part of the product surface; plans are minute-granular.
var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// NextRuns resolves the next occurrences of a cron expression inside an IANA
// timezone. The location is carried on the evaluation time so field matching
// follows local wall-clock days, including DST shifts.
func NextRuns(spec, timezone string, after time.Time, count int) ([]time.Time, error) {
	if count <= 0 {
		return nil, nil
	}
	if _, err := LoadLocation(timezone); err != nil {
		return nil, err
	}
	schedule, err := parser.Parse(spec)
	if err != nil {
		return nil, err
	}
	location, _ := LoadLocation(timezone)
	result := make([]time.Time, 0, count)
	current := after.In(location)
	for len(result) < count {
		next := schedule.Next(current)
		if next.IsZero() || !next.After(after) {
			break
		}
		result = append(result, next.UTC())
		current = next
	}
	return result, nil
}
