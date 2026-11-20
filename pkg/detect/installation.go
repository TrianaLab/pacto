package detect

import "time"

// PushResult is the last target snapshot push the operator attempted: when,
// to which ref and, on failure, why. A failed push is never shown as healthy.
type PushResult struct {
	At    time.Time `json:"at,omitzero"`
	Ref   string    `json:"ref,omitempty"`
	Error string    `json:"error,omitempty"`
}
