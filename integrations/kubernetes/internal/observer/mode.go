/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package observer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// LabelObserve is set on a Namespace by its owner; its value is probing, metrics or probing.metrics.
// The operator reads it only when it runs that feature in auto mode.
const LabelObserve = "pacto.trianalab.io/observe"

// Mode is how the operator runs an observation feature: on, off or auto (per namespace, from
// LabelObserve). The zero value reads as off.
type Mode string

const (
	ModeAuto Mode = "auto"
	ModeOn   Mode = "on"
	ModeOff  Mode = "off"
)

// Set implements flag.Value; true and false are accepted for the boolean flag the mode replaced.
func (m *Mode) Set(s string) error {
	switch s {
	case "true", "on":
		*m = ModeOn
	case "false", "off":
		*m = ModeOff
	case "auto":
		*m = ModeAuto
	default:
		return fmt.Errorf("invalid mode %q: use on, off or auto", s)
	}
	return nil
}

func (m *Mode) String() string {
	if *m == "" {
		return string(ModeOff)
	}
	return string(*m)
}

// IsBoolFlag makes the bare flag mean on.
func (m *Mode) IsBoolFlag() bool { return true }

// Setting is a feature resolved for one namespace and where that answer came from.
type Setting struct {
	On     bool
	Source string
}

// Resolve settles mode for one namespace: auto is on only when the namespace's LabelObserve value
// names feature. An unreadable namespace (readErr) or an unknown label reads off, never on.
func Resolve(m Mode, flagName, feature string, labels map[string]string, readErr error) Setting {
	switch m {
	case ModeOn:
		return Setting{On: true, Source: "--" + flagName + "=on"}
	case ModeAuto:
	default:
		return Setting{Source: "--" + flagName + "=off"}
	}
	auto := "--" + flagName + "=auto, "
	if readErr != nil {
		return Setting{Source: auto + "namespace label unreadable: " + readReason(readErr)}
	}
	value, ok := labels[LabelObserve]
	if !ok {
		return Setting{Source: auto + "namespace has no " + LabelObserve + " label"}
	}
	s := Setting{Source: auto + "namespace label " + LabelObserve + "=" + value + " does not name " + feature}
	var unknown []string
	for t := range strings.SplitSeq(value, ".") {
		switch t {
		case feature:
			s = Setting{On: true, Source: "namespace label " + LabelObserve + "=" + value}
		case "probing", "metrics":
		default:
			if !slices.Contains(unknown, t) {
				unknown = append(unknown, t)
			}
		}
	}
	for _, t := range unknown {
		s.Source += "; unknown observe value " + t + " ignored"
	}
	return s
}

// readReason maps a namespace read failure onto a fixed word, so the condition message never carries
// raw error text and stays deterministic.
func readReason(err error) string {
	switch {
	case apierrors.IsForbidden(err):
		return "forbidden"
	case apierrors.IsNotFound(err):
		return "not found"
	case apierrors.IsTimeout(err), apierrors.IsServerTimeout(err), errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "unavailable"
	}
}
