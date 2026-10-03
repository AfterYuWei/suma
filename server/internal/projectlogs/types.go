package projectlogs

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"time"
)

const MaxSources = 64
const MaxBytes = 8 << 20
const MaxLineBytes = 64 << 10

var ErrInvalid = errors.New("invalid project log query")
var ErrTooManySources = errors.New("select no more than 64 container instances")
var projectName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

type Source struct {
	ContainerID   string `json:"container_id"`
	ContainerName string `json:"container_name"`
	Service       string `json:"service"`
	Instance      int    `json:"instance"`
	State         string `json:"state"`
	OneOff        bool   `json:"one_off"`
	Orphan        bool   `json:"orphan"`
}
type Record struct {
	ID            string    `json:"id"`
	Time          time.Time `json:"time"`
	Service       string    `json:"service"`
	ContainerID   string    `json:"container_id"`
	ContainerName string    `json:"container_name"`
	Stream        string    `json:"stream"`
	Text          string    `json:"text"`
	Truncated     bool      `json:"truncated,omitempty"`
}
type Query struct {
	Tail                 int
	Since, Until         string
	Services, Containers []string
	IncludeOneOff        bool
	Follow               bool
}

func (q Query) Validate() error {
	if q.Tail < 1 || q.Tail > 5000 {
		return ErrInvalid
	}
	var since, until time.Time
	var err error
	if q.Since != "" {
		since, err = time.Parse(time.RFC3339Nano, q.Since)
		if err != nil {
			return ErrInvalid
		}
	}
	if q.Until != "" {
		until, err = time.Parse(time.RFC3339Nano, q.Until)
		if err != nil || q.Follow {
			return ErrInvalid
		}
	}
	if !since.IsZero() && !until.IsZero() && !since.Before(until) {
		return ErrInvalid
	}
	if len(q.Containers) > MaxSources || len(q.Services) > MaxSources {
		return ErrTooManySources
	}
	return nil
}

type SourceError struct {
	ContainerID string `json:"container_id"`
	Message     string `json:"message"`
}
type Snapshot struct {
	Entries   []Record      `json:"entries"`
	Sources   []Source      `json:"sources"`
	Errors    []SourceError `json:"errors"`
	Truncated bool          `json:"truncated"`
}
type Event struct {
	Type      string        `json:"type"`
	Entries   []Record      `json:"entries,omitempty"`
	Sources   []Source      `json:"sources,omitempty"`
	Errors    []SourceError `json:"errors,omitempty"`
	Truncated bool          `json:"truncated,omitempty"`
}
type Runtime interface {
	ProjectLogSources(context.Context, string) ([]Source, error)
	ReadProjectLogs(context.Context, Source, Query, func(Record) error) error
}
type Dependencies struct {
	Runtime    func(context.Context, string) (Runtime, error)
	RuntimeKey func(context.Context, string) (string, error)
	Exists     func(context.Context, string, string) error
	Declared   func(context.Context, string, string) (map[string]bool, error)
}
type buffer struct {
	entries   []Record
	bytes     int
	truncated bool
	tail      int
}

func (b *buffer) add(r Record) {
	index := sort.Search(len(b.entries), func(i int) bool {
		a := b.entries[i]
		if a.Time.Equal(r.Time) {
			return a.ID >= r.ID
		}
		return a.Time.After(r.Time)
	})
	b.entries = append(b.entries, Record{})
	copy(b.entries[index+1:], b.entries[index:])
	b.entries[index] = r
	b.bytes += recordBytes(r)
	for len(b.entries) > b.tail || b.bytes > MaxBytes {
		b.bytes -= recordBytes(b.entries[0])
		b.entries = b.entries[1:]
		b.truncated = true
	}
	if r.Truncated {
		b.truncated = true
	}
}

func recordBytes(r Record) int {
	return len(r.Text) + len(r.ID) + len(r.ContainerName) + len(r.ContainerID) + len(r.Service) + len(r.Stream) + 256
}
