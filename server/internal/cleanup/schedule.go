package cleanup

import (
	"fmt"
	"strings"
	"time"
	_ "time/tzdata"
)

func DefaultConfig(zone string) Config {
	if _, err := time.LoadLocation(zone); err != nil || zone == "Local" || zone == "" {
		zone = "UTC"
	}
	return Config{
		Schedule:   Schedule{Frequency: "weekly", Weekday: 0, Hour: 3, Timezone: zone},
		Images:     ImageRule{Rule: Rule{Enabled: true, RetentionDays: 7}},
		Cache:      CacheRule{Rule: Rule{Enabled: true, RetentionDays: 7}, ReservedBytes: 10 * 1024 * 1024 * 1024},
		Containers: Rule{RetentionDays: 7}, Networks: Rule{RetentionDays: 7},
		ScanVolumes: true, Protected: map[Kind][]string{},
	}
}
func Validate(c Config) error {
	s := c.Schedule
	if (s.Frequency != "daily" && s.Frequency != "weekly") || s.Weekday < 0 || s.Weekday > 6 || s.Hour < 0 || s.Hour > 23 || s.Minute < 0 || s.Minute > 59 || s.Timezone == "Local" || s.Timezone == "" {
		return ErrInvalid
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return fmt.Errorf("%w: timezone", ErrInvalid)
	}
	for _, r := range []Rule{c.Images.Rule, c.Cache.Rule, c.Containers, c.Networks} {
		if r.RetentionDays < 1 || r.RetentionDays > 3650 {
			return fmt.Errorf("%w: retention must be 1–3650 days", ErrInvalid)
		}
	}
	if c.Cache.ReservedBytes < 0 || c.Cache.ReservedBytes > 1024*1024*1024*1024*1024 {
		return fmt.Errorf("%w: cache budget", ErrInvalid)
	}
	count := 0
	for k, values := range c.Protected {
		if k != Image && k != Container && k != Network && k != Volume {
			return fmt.Errorf("%w: protection kind", ErrInvalid)
		}
		for _, v := range values {
			count++
			if strings.TrimSpace(v) != v || v == "" || len(v) > 512 || strings.ContainsAny(v, "\x00\n\r") {
				return fmt.Errorf("%w: protected identifier", ErrInvalid)
			}
		}
	}
	if count > 500 {
		return fmt.Errorf("%w: too many protected resources", ErrInvalid)
	}
	return nil
}

// Resolve each civil date independently. A missing DST minute is skipped; the
// earliest matching UTC minute is chosen when the clock repeats. Advancing by
// civil date ensures the repeated occurrence can never execute twice.
func NextOccurrences(s Schedule, after time.Time, count int) []time.Time {
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return []time.Time{}
	}
	local := after.In(loc)
	day := time.Date(local.Year(), local.Month(), local.Day(), 12, 0, 0, 0, loc)
	result := []time.Time{}
	for i := 0; i < 370 && len(result) < count; i++ {
		if s.Frequency == "daily" || int(day.Weekday()) == s.Weekday {
			approximate := time.Date(day.Year(), day.Month(), day.Day(), s.Hour, s.Minute, 0, 0, loc)
			var first time.Time
			// Covers even the three-hour historical transitions and date-line shifts.
			for t := approximate.Add(-26 * time.Hour); !t.After(approximate.Add(26 * time.Hour)); t = t.Add(time.Minute) {
				l := t.In(loc)
				if l.Year() == day.Year() && l.Month() == day.Month() && l.Day() == day.Day() && l.Hour() == s.Hour && l.Minute() == s.Minute {
					first = t.UTC()
					break
				}
			}
			if !first.IsZero() && first.After(after) {
				result = append(result, first)
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	return result
}
func expanded(old, next Config) bool {
	for _, pair := range [][2]Rule{{old.Images.Rule, next.Images.Rule}, {old.Cache.Rule, next.Cache.Rule}, {old.Containers, next.Containers}, {old.Networks, next.Networks}} {
		if pair[1].Enabled && (!pair[0].Enabled || pair[1].RetentionDays < pair[0].RetentionDays) {
			return true
		}
	}
	if next.Images.Enabled && next.Images.IncludeTagged && !old.Images.IncludeTagged {
		return true
	}
	if next.Cache.Enabled && next.Cache.ReservedBytes < old.Cache.ReservedBytes {
		return true
	}
	for k, values := range old.Protected {
		for _, v := range values {
			found := false
			for _, n := range next.Protected[k] {
				found = found || v == n
			}
			if !found {
				return true
			}
		}
	}
	return false
}
