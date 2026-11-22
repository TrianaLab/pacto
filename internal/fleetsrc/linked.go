package fleetsrc

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"time"

	"k8s.io/client-go/dynamic"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// LinkedSource is a [fleet.Source] over the workloads the Pacto operator
// linked to a published contract with no Pacto: the linked.json key of its
// <fullname>-detected ConfigMap, or a file holding the same JSON. Each linked
// workload is a target of its contract's revision, evaluated by the operator,
// with its link. Collect never fails: a missing, unreadable, foreign or
// future-dated payload is an unavailable state that names why, and one older
// than [delivery.LinkedStaleAfter] is stale.
type LinkedSource struct {
	id   string
	read func(context.Context) ([]byte, error)
	now  func() time.Time
}

// NewLinkedFileSource reads the linked targets in the file at path.
func NewLinkedFileSource(id, path string, now func() time.Time) *LinkedSource {
	return &LinkedSource{id: id, now: now, read: func(context.Context) ([]byte, error) { return os.ReadFile(path) }}
}

// NewLinkedK8sSource reads the linked targets in the ConfigMap namespace/name
// with dyn, the caller's own credentials. It trusts the key only on a
// ConfigMap labelled as the operator's whose key the linker's field manager
// owns.
func NewLinkedK8sSource(id string, dyn dynamic.Interface, namespace, name string, now func() time.Time) *LinkedSource {
	return &LinkedSource{id: id, now: now, read: configMapKey(dyn, namespace, name, delivery.LinkedConfigMapKey, delivery.LinkerFieldManager)}
}

// NewLinkedFailedSource is the source of a ConfigMap no client could be built
// to read: always unavailable with err's class.
func NewLinkedFailedSource(id string, err error) *LinkedSource {
	return &LinkedSource{id: id, now: time.Now, read: func(context.Context) ([]byte, error) { return nil, err }}
}

// ID is the source id.
func (s *LinkedSource) ID() string { return s.id }

// Kind is [fleet.SourceKindLinked].
func (s *LinkedSource) Kind() string { return fleet.SourceKindLinked }

// Collect reads the linked targets. It never returns an error.
func (s *LinkedSource) Collect(ctx context.Context) (*fleet.Collection, error) {
	l, err := s.linked(ctx)
	if err != nil {
		return &fleet.Collection{State: &fleet.SourceState{Status: fleet.SourceUnavailable, Error: linkedError(err)}}, nil
	}
	now := s.now()
	col := delivery.LinkedCollection(s.id, l.Targets, now)
	if l.Cluster != "" { // empty is an unknown cluster, never another one
		col.Cluster = &fleet.Cluster{ID: l.Cluster, Name: l.Cluster[:min(8, len(l.Cluster))], NameSource: fleet.ClusterNameFromUID}
	}
	asOf, _ := time.Parse(time.RFC3339, l.AsOf) // DecodeLinked checked it
	st := &fleet.SourceState{Status: fleet.SourceAvailable, ObservedAt: &asOf, LastSuccessfulSync: &asOf}
	for _, lim := range l.Limitations {
		if lim.Code == delivery.LimitationLinkDigestOff {
			// Informational: the configured signals were all checked, so
			// the source stays available and says so in its state.
			st.Error = &fleet.SourceError{Code: lim.Code, Message: linkDigestOffMessage}
			continue
		}
		lim.Source = s.id
		col.Limitations = append(col.Limitations, lim)
	}
	switch {
	case now.Sub(asOf) > delivery.LinkedStaleAfter:
		st.Status = fleet.SourceStale
	case l.Completeness == fleet.CompletenessPartial:
		st.Status = fleet.SourcePartial
	}
	col.State = st
	return col, nil
}

// linkDigestOffMessage is the state message of a linker with no registry.
const linkDigestOffMessage = "linking by image digest is off: the operator has no registry, so only workloads annotated with pacto.trianalab.io/contract are linked"

// linked reads and decodes the linked targets, refusing a payload dated
// beyond detect.MaxFutureSkew.
func (s *LinkedSource) linked(ctx context.Context) (delivery.LinkedTargets, error) {
	data, err := s.read(ctx)
	if err != nil {
		return delivery.LinkedTargets{}, err
	}
	l, err := delivery.DecodeLinked(data)
	if err != nil {
		return delivery.LinkedTargets{}, errDetectionInvalid
	}
	if asOf, _ := time.Parse(time.RFC3339, l.AsOf); asOf.After(s.now().Add(detect.MaxFutureSkew)) {
		return delivery.LinkedTargets{}, errDetectionInvalid
	}
	return l, nil
}

// linkedError names why the linked targets could not be read, never with the
// raw error text.
func linkedError(err error) *fleet.SourceError {
	switch {
	case errors.Is(err, errDetectionInvalid):
		return &fleet.SourceError{Code: "INVALID", Message: "invalid: the linked workloads are unreadable or dated in the future"}
	case errors.Is(err, errDetectionForeign):
		return &fleet.SourceError{Code: "UNTRUSTED", Message: "not written by the Pacto operator's linker"}
	case errors.Is(err, ErrDetectionNoCluster):
		return &fleet.SourceError{Code: "NO_CLUSTER", Message: "no usable Kubernetes configuration to read the workloads the operator linked"}
	case errors.Is(err, fs.ErrNotExist) || reasonClass(err) == "not found":
		return &fleet.SourceError{Code: "NOT_FOUND", Message: "not found: the operator has written no linked workloads yet"}
	case errors.Is(err, fs.ErrPermission) || reasonClass(err) == "forbidden":
		return &fleet.SourceError{Code: "AUTH_FAILED", Message: "forbidden: you may not read the workloads the operator linked"}
	}
	return &fleet.SourceError{Code: "UNAVAILABLE", Message: "the workloads the operator linked could not be read"}
}
