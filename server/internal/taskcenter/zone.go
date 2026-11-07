package taskcenter

import (
	"errors"
	"time"
	_ "time/tzdata"
)

// LoadLocation resolves an IANA timezone. "Local" and the empty value are
// rejected so plans never depend on the server's own zone.
func LoadLocation(name string) (*time.Location, error) {
	if name == "" || name == "Local" {
		return nil, errors.New("timezone is required")
	}
	return time.LoadLocation(name)
}
