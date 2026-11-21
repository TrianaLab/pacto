package fleetsrc

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// DetectionSource is an advisory [fleet.Source] over what the Pacto operator
// detected in a cluster: its <fullname>-detected ConfigMap, or a file holding
// the same JSON. It contributes only inferred provider rows. Collect never
// fails: a missing, unreadable, foreign or future-dated Result is an
// unavailable state that names why, and a Result older than
// [detect.StaleAfter] is stale. Neither makes a snapshot partial.
type DetectionSource struct {
	id   string
	read func(context.Context) ([]byte, error)
	now  func() time.Time
	// fixed is a Result already read, reported as is.
	fixed *detect.Result
}

// errDetectionInvalid and errDetectionForeign are the classes of a Result
// the source refuses.
var (
	errDetectionInvalid = errors.New("invalid")
	errDetectionForeign = errors.New("not written by the Pacto operator")
	// ErrDetectionNoCluster marks a detection read that has no usable
	// Kubernetes configuration to read the ConfigMap with.
	ErrDetectionNoCluster = errors.New("no usable Kubernetes configuration")
)

// NewDetectionFileSource reads the Result in the file at path.
func NewDetectionFileSource(id, path string, now func() time.Time) *DetectionSource {
	return &DetectionSource{id: id, now: now, read: func(context.Context) ([]byte, error) { return os.ReadFile(path) }}
}

// NewDetectionK8sSource reads the Result in the ConfigMap namespace/name with
// dyn, the caller's own credentials. It trusts only a ConfigMap labelled as
// the operator's whose data the operator's field manager owns.
func NewDetectionK8sSource(id string, dyn dynamic.Interface, namespace, name string, now func() time.Time) *DetectionSource {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	return &DetectionSource{id: id, now: now, read: func(ctx context.Context) ([]byte, error) {
		obj, err := dyn.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		if !writtenByOperator(obj) {
			return nil, errDetectionForeign
		}
		data, _, _ := unstructured.NestedString(obj.Object, "data", detect.ConfigMapKey)
		return []byte(data), nil
	}}
}

// NewDetectionResultSource reports res, already read, through the same
// mapping as [DetectionSource.Collect]: the dashboard reads the Result once,
// hands the entries a live edge source takes to that source and the rest to
// this one, so every backend is one row.
func NewDetectionResultSource(id string, res detect.Result, now func() time.Time) *DetectionSource {
	return &DetectionSource{id: id, now: now, fixed: &res}
}

// NewDetectionFailedSource is the source of a ConfigMap no client could be
// built to read: always unavailable with err's class.
func NewDetectionFailedSource(id string, err error) *DetectionSource {
	return &DetectionSource{id: id, now: time.Now, read: func(context.Context) ([]byte, error) { return nil, err }}
}

// ID is the source id.
func (s *DetectionSource) ID() string { return s.id }

// Kind is [fleet.SourceKindDetection].
func (s *DetectionSource) Kind() string { return fleet.SourceKindDetection }

// Collect reads the Result. It never returns an error.
func (s *DetectionSource) Collect(ctx context.Context) (*fleet.Collection, error) {
	res, state := s.Read(ctx)
	col := &fleet.Collection{Advisory: true, State: &state}
	if state.Status != fleet.SourceUnavailable {
		col.Providers = detect.ProviderRows(res, s.id, s.now())
	}
	return col, nil
}

// Read is the decoded Result with the source's state: unavailable with why
// when it could not be read, stale when older than [detect.StaleAfter],
// available otherwise. Collect and pacto ci's installation advertisement both
// read through it, so there is one reader.
func (s *DetectionSource) Read(ctx context.Context) (detect.Result, fleet.SourceState) {
	res, err := s.result(ctx)
	if err != nil {
		return detect.Result{}, fleet.SourceState{Status: fleet.SourceUnavailable, Error: detectionError(err)}
	}
	status := fleet.SourceAvailable
	if s.now().Sub(res.AsOf) > detect.StaleAfter {
		status = fleet.SourceStale
	}
	asOf := res.AsOf
	return res, fleet.SourceState{Status: status, ObservedAt: &asOf, LastSuccessfulSync: &asOf}
}

// result reads and decodes the Result, refusing one dated beyond
// detect.MaxFutureSkew.
func (s *DetectionSource) result(ctx context.Context) (detect.Result, error) {
	if s.fixed != nil {
		return *s.fixed, nil
	}
	data, err := s.read(ctx)
	if err != nil {
		return detect.Result{}, err
	}
	res, err := detect.Decode(data)
	if err != nil {
		return detect.Result{}, errDetectionInvalid
	}
	if res.AsOf.After(s.now().Add(detect.MaxFutureSkew)) {
		return detect.Result{}, errDetectionInvalid
	}
	return res, nil
}

// writtenByOperator reports whether obj carries the operator's label and its
// data is owned by the operator's field manager.
func writtenByOperator(obj *unstructured.Unstructured) bool {
	if obj.GetLabels()[detect.ManagedByLabel] != detect.ManagedBy {
		return false
	}
	for _, mf := range obj.GetManagedFields() {
		if mf.Manager == detect.FieldManager && mf.FieldsV1 != nil && strings.Contains(mf.FieldsV1.GetRawString(), `"f:data"`) {
			return true
		}
	}
	return false
}

// detectionError names why a Result could not be read, never with its raw
// text: forbidden, not found, invalid, not written by the Pacto operator or
// no usable Kubernetes configuration.
func detectionError(err error) *fleet.SourceError {
	switch {
	case errors.Is(err, errDetectionInvalid):
		return &fleet.SourceError{Code: "INVALID", Message: "invalid: the detection is unreadable or dated in the future"}
	case errors.Is(err, errDetectionForeign):
		return &fleet.SourceError{Code: "UNTRUSTED", Message: "not written by the Pacto operator"}
	case errors.Is(err, ErrDetectionNoCluster):
		return &fleet.SourceError{Code: "NO_CLUSTER", Message: "no usable Kubernetes configuration to read what the operator detected"}
	case errors.Is(err, fs.ErrNotExist) || reasonClass(err) == "not found":
		return &fleet.SourceError{Code: "NOT_FOUND", Message: "not found: the operator has not published detection yet"}
	case errors.Is(err, fs.ErrPermission) || reasonClass(err) == "forbidden":
		return &fleet.SourceError{Code: "AUTH_FAILED", Message: "forbidden: you may not read what the operator detected"}
	}
	return &fleet.SourceError{Code: "UNAVAILABLE", Message: "what the operator detected could not be read"}
}
