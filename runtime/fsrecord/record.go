// Package fsrecord defines the lightweight filesystem recording boundary.
// It has no database or replay dependency.
package fsrecord

import "encoding/json"

// Event describes an operation at the Host filesystem adapter, not a proof
// that each returned byte influenced Python or reached the model.
type Event struct {
	Seq       uint64          `json:"seq"`
	Operation string          `json:"operation"`
	Path      string          `json:"path"`
	Resource  string          `json:"resource,omitempty"`
	Version   uint64          `json:"version,omitempty"`
	Offset    int64           `json:"offset,omitempty"`
	Count     int64           `json:"count,omitempty"`
	Error     string          `json:"error,omitempty"`
	Detail    json.RawMessage `json:"detail,omitempty"`
}

// Sink belongs to a single RunFiles invocation and is finalized by that call.
// Append may batch writes. A sink must not claim crash-durable replay unless it
// independently implements that stronger contract. Failures terminate recording.
// EnsureContent may ignore load for metadata-only capture. Content identity is
// supplied by a trusted immutable provider, not computed from a guest path.
type Sink interface {
	ViewRevision() string
	Append(Event) error
	EnsureContent(resource string, version uint64, load func() ([]byte, error)) error
	Finish(runError error) error
}
