package fleetsrc

import (
	"cmp"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/evidenceingest"
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
	// Contributions reads the inferred offerings the installation
	// advertises, verified with its installation key; nil reads none.
	Contributions ContributionReader
	// Freshness is the producer row's freshness window; 0 checks no age.
	Freshness time.Duration
}

// errDetectionInvalid and errDetectionForeign are the classes of a Result
// the source refuses.
var (
	errDetectionInvalid = errors.New("invalid")
	errDetectionForeign = errors.New("not written by the Pacto operator")
	// errNoWriter is an absent key on a ConfigMap its writer's field manager
	// has never touched: an operator that does not run that writer.
	errNoWriter = fmt.Errorf("no writer: %w", fs.ErrNotExist)
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
	return &DetectionSource{id: id, now: now, read: configMapKey(dyn, namespace, name, detect.ConfigMapKey, detect.FieldManager)}
}

// configMapKey reads key of the ConfigMap namespace/name with dyn. It trusts
// the key only on a ConfigMap labelled as the operator's whose manager owns
// that key: each operator component writes its own key under its own field
// manager, so a key another writer patched in is refused. An absent key reads
// as not found, and as errNoWriter when manager manages nothing on it.
func configMapKey(dyn dynamic.Interface, namespace, name, key, manager string) func(context.Context) ([]byte, error) {
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	return func(ctx context.Context) ([]byte, error) {
		obj, err := dyn.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		data, ok, _ := unstructured.NestedString(obj.Object, "data", key)
		switch {
		case !ok && !slices.ContainsFunc(obj.GetManagedFields(), func(mf metav1.ManagedFieldsEntry) bool { return mf.Manager == manager }):
			return nil, errNoWriter
		case !ok:
			return nil, fs.ErrNotExist
		case !writtenByOperator(obj, key, manager):
			return nil, errDetectionForeign
		}
		return []byte(data), nil
	}
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
		s.offerings(ctx, res, col)
	}
	return col, nil
}

// offerings reads the inferred offerings res's installation advertises into
// col: through s.Contributions, only from the installation's own repository
// (see [detect.Result.OfferingsRepository]) and verified only with the
// installation key it advertises, as one producer row. Every revision read
// is marked with the cluster it was advertised in. A repository that is not
// the installation's own is never read and says so; a partial advertisement
// makes the producer row partial. Without an advertisement, or a reader,
// nothing changes.
func (s *DetectionSource) offerings(ctx context.Context, res detect.Result, col *fleet.Collection) {
	if s.Contributions == nil || res.Installation == nil || res.Installation.Offerings == nil {
		return
	}
	inst, o := res.Installation, res.Installation.Offerings
	obs := fleet.OfferingObservation{Producer: o.Producer, Cluster: res.Cluster, Observed: slices.Clone(o.Observed), AsOf: o.AsOf, Partial: o.Partial}
	repo, ok := res.OfferingsRepository()
	if !ok {
		obs.Unread = "the advertised repository is not this installation's own"
		col.Offerings = append(col.Offerings, obs)
		col.Notes = append(col.Notes, fleet.Limitation{Code: fleet.LimitationContributionUnread, Source: s.id,
			Message: "the inferred offerings the installation advertises were not read: " + obs.Unread})
		return
	}
	f := fleet.ProducerFacts{Producer: o.Producer}
	c := evidenceingest.ProducerContributor{Producer: o.Producer, Repository: repo}
	if raw, ok := rawEd25519(inst.SnapshotKey); ok {
		c.Keys = []evidenceingest.ContributorKey{{KeyID: inst.SnapshotKeyID, PublicKey: raw}}
	}
	revs, lims := s.Contributions.Read(ctx, s.id, c, &f)
	for i := range revs {
		revs[i].Contributor.Cluster = res.Cluster
	}
	if o.Partial != "" {
		f.Problems = append(f.Problems, "the producer's last run stopped short: "+o.Partial)
		lims = append(lims, fleet.Limitation{Code: fleet.LimitationContributionUnread, Source: s.id,
			Message: "the inferred offerings of " + o.Producer + " are partial: " + o.Partial})
	}
	if len(lims) > 0 {
		obs.Unread = lims[0].Message
	}
	col.Revisions = append(col.Revisions, revs...)
	col.Notes = append(col.Notes, lims...)
	col.Offerings = append(col.Offerings, obs)
	if row, ok := fleet.ProducerRow(f, s.now(), s.Freshness); ok {
		row.Cluster, row.ClusterID = cmp.Or(res.ClusterName, res.Cluster), res.Cluster
		col.Providers = append(col.Providers, row)
	}
}

// rawEd25519 is the base64 of the raw Ed25519 public key whose PKIX DER
// encoded is the base64 of, as a contribution reader takes it; false for
// any other key.
func rawEd25519(encoded string) (string, bool) {
	der, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", false
	}
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return "", false
	}
	ed, ok := pub.(ed25519.PublicKey)
	return base64.StdEncoding.EncodeToString(ed), ok
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

// writtenByOperator reports whether obj carries the operator's label and
// manager owns its data key.
func writtenByOperator(obj *unstructured.Unstructured, key, manager string) bool {
	if obj.GetLabels()[detect.ManagedByLabel] != detect.ManagedBy {
		return false
	}
	for _, mf := range obj.GetManagedFields() {
		var fields struct {
			Data map[string]json.RawMessage `json:"f:data"`
		}
		if mf.Manager == manager && mf.FieldsV1 != nil && json.Unmarshal(mf.FieldsV1.GetRawBytes(), &fields) == nil && fields.Data["f:"+key] != nil {
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
