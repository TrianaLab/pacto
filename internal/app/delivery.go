package app

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/logging"
)

// Delivery resources the client reads and writes.
var (
	gvrStages        = schema.GroupVersionResource{Group: "delivery.pacto.trianalab.io", Version: "v1alpha1", Resource: "stages"}
	gvrOperations    = schema.GroupVersionResource{Group: "delivery.pacto.trianalab.io", Version: "v1alpha1", Resource: "operations"}
	gvrApprovals     = schema.GroupVersionResource{Group: "delivery.pacto.trianalab.io", Version: "v1alpha1", Resource: "approvals"}
	gvrPactos        = schema.GroupVersionResource{Group: "pacto.trianalab.io", Version: "v1alpha1", Resource: "pactos"}
	gvrOCIRepository = schema.GroupVersionResource{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "ocirepositories"}
	gvrKustomization = schema.GroupVersionResource{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Resource: "kustomizations"}
	gvrSelfReview    = schema.GroupVersionResource{Group: "authentication.k8s.io", Version: "v1", Resource: "selfsubjectreviews"}
	gvrNamespaces    = schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
	gvrConfigMaps    = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
)

// recordFinalizer is the finalizer admission requires on every Operation.
const recordFinalizer = "delivery.pacto.trianalab.io/record"

// authTimeExtra is the userInfo.extra key structured authentication maps the
// OIDC auth_time claim to.
const authTimeExtra = "delivery.pacto.trianalab.io/auth-time"

// DeliveryRegistry is the registry access a delivery client needs: the
// promoter's read-only access, plus resolving a tag so a refusal can name the
// digest it points at. *oci.Client implements it.
type DeliveryRegistry interface {
	delivery.Registry
	Resolve(ctx context.Context, ref string) (string, error)
}

// Delivery is the client side of Pacto delivery: plan, propose, get, cancel and
// approve against a cluster running the pacto-delivery promoter. It computes
// plans with the same pure function the promoter runs, so a plan predicts what
// the promoter will do with the same cluster and registry state.
type Delivery struct {
	Dynamic dynamic.Interface
	// Registry returns a registry client that reaches insecureHosts over HTTP.
	Registry func(insecureHosts ...string) DeliveryRegistry
	// Policy classifies the caller when the promoter's policy is not
	// published; ControllerUser is unused on the client.
	Policy delivery.ClassPolicy
	// PolicyNamespace is where the delivery chart publishes the promoter's
	// ClassPolicy (delivery.PolicyConfigMap); empty uses Policy as is.
	PolicyNamespace string
	Now             func() time.Time
	// Records opens repositories to read operation records from; nil reads
	// none, and history comes from the cluster only.
	Records RecordRepo
	// Trust verifies records; nil lists them unverified and says so.
	Trust *delivery.TrustConfig
}

// Caller is who the apiserver says is calling, and when they last logged in.
type Caller = delivery.Caller

// WhoAmI asks the apiserver who the caller is (a SelfSubjectReview), so an
// operation's actor is exactly what admission will compare it with.
func (d *Delivery) WhoAmI(ctx context.Context) (Caller, error) {
	review := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "authentication.k8s.io/v1", "kind": "SelfSubjectReview",
	}}
	out, err := d.Dynamic.Resource(gvrSelfReview).Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return Caller{}, fmt.Errorf("asking the apiserver who you are: %w", err)
	}
	var r struct {
		Status struct {
			UserInfo struct {
				Username string              `json:"username"`
				Groups   []string            `json:"groups"`
				Extra    map[string][]string `json:"extra"`
			} `json:"userInfo"`
		} `json:"status"`
	}
	b, _ := out.MarshalJSON()
	_ = json.Unmarshal(b, &r)
	ui := r.Status.UserInfo
	c := Caller{Identity: delivery.Identity{User: ui.Username, Groups: ui.Groups}, Policy: d.classPolicy(ctx)}
	c.Class = c.Policy.Classify(c.Identity)
	if v := ui.Extra[authTimeExtra]; len(v) > 0 {
		c.AuthTime = v[0]
	}
	return c, nil
}

// classPolicy is the promoter's published ClassPolicy, or Policy when it is
// not published or cannot be read. Admission and the promoter decide anyway;
// this only shapes what a client offers.
// ponytail: read on every WhoAmI; cache it if the extra GET ever shows up.
func (d *Delivery) classPolicy(ctx context.Context) delivery.ClassPolicy {
	if d.PolicyNamespace == "" {
		return d.Policy
	}
	var cm struct {
		Data map[string]string `json:"data"`
	}
	err := getInto(ctx, d.Dynamic.Resource(gvrConfigMaps).Namespace(d.PolicyNamespace), delivery.PolicyConfigMap, &cm)
	if err == nil {
		var p delivery.ClassPolicy
		if p, err = delivery.ParseClassPolicy([]byte(cm.Data[delivery.PolicyKey])); err == nil {
			return p
		}
	}
	logging.LoggerFromContext(ctx).Warn("the delivery identity policy is not readable; classifying with the defaults",
		"namespace", d.PolicyNamespace, "configMap", delivery.PolicyConfigMap, "error", err)
	return d.Policy
}

func anySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func getInto[T any](ctx context.Context, ri dynamic.ResourceInterface, name string, into *T) error {
	u, err := ri.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	b, _ := u.MarshalJSON()
	return json.Unmarshal(b, into)
}

func listInto[T any](ctx context.Context, r dynamic.NamespaceableResourceInterface, ns, what string) ([]T, error) {
	var ri dynamic.ResourceInterface = r
	if ns != "" {
		ri = r.Namespace(ns)
	}
	list, err := ri.List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing %s in %s: %w", what, cmp.Or(ns, "every namespace"), err)
	}
	var out struct {
		Items []T `json:"items"`
	}
	b, _ := list.MarshalJSON()
	_ = json.Unmarshal(b, &out)
	return out.Items, nil
}

// Stages lists the Stages in a namespace, or in every namespace for "".
func (d *Delivery) Stages(ctx context.Context, ns string) ([]delivery.StageObject, error) {
	return listInto[delivery.StageObject](ctx, d.Dynamic.Resource(gvrStages), ns, "Stages")
}

// Operations lists the Operations in a namespace, or in every namespace for "".
func (d *Delivery) Operations(ctx context.Context, ns string) ([]delivery.OperationObject, error) {
	return listInto[delivery.OperationObject](ctx, d.Dynamic.Resource(gvrOperations), ns, "Operations")
}

// Describe reads what one of a Stage's artifact digests carries: the
// contract it names and that contract. Failures are in Revision.Err.
func (d *Delivery) Describe(ctx context.Context, stage *delivery.StageObject, digest string) delivery.Revision {
	repo, reg, err := d.applyRegistry(ctx, stage)
	if err != nil {
		return delivery.Revision{Digest: digest, Err: err.Error()}
	}
	return delivery.ResolveRevision(ctx, reg, repo, digest)
}

// Stage reads one Stage.
func (d *Delivery) Stage(ctx context.Context, ns, name string) (*delivery.StageObject, error) {
	s := &delivery.StageObject{}
	if err := getInto(ctx, d.Dynamic.Resource(gvrStages).Namespace(ns), name, s); err != nil {
		return nil, fmt.Errorf("reading Stage %s/%s: %w", ns, name, err)
	}
	return s, nil
}

// Get reads one Operation.
func (d *Delivery) Get(ctx context.Context, ns, name string) (*delivery.OperationObject, error) {
	o := &delivery.OperationObject{}
	if err := getInto(ctx, d.Dynamic.Resource(gvrOperations).Namespace(ns), name, o); err != nil {
		return nil, fmt.Errorf("reading Operation %s/%s: %w", ns, name, err)
	}
	return o, nil
}

// PlanStaleError is a propose refused because something moved since the plan
// the caller made. Plan is the fresh one.
type PlanStaleError struct{ Plan delivery.Plan }

func (e *PlanStaleError) Error() string {
	return "PlanStale: the Stage moved since you planned (intent digest is now " + e.Plan.IntentDigest + "); review the fresh plan and propose again"
}

// Plan builds the operation a request describes and plans it.
func (d *Delivery) Plan(ctx context.Context, req delivery.Request) (delivery.Plan, delivery.OperationSpec, error) {
	stage, err := d.Stage(ctx, req.Namespace, req.Stage)
	if err != nil {
		return delivery.Plan{}, delivery.OperationSpec{}, err
	}
	caller, err := d.WhoAmI(ctx)
	if err != nil {
		return delivery.Plan{}, delivery.OperationSpec{}, err
	}
	spec, err := d.spec(ctx, req, stage, caller)
	if err != nil {
		return delivery.Plan{}, delivery.OperationSpec{}, err
	}
	in, err := delivery.AssemblePlanInput(ctx, planSources{d}, spec, *stage, caller.Class, d.Now())
	if err != nil {
		return delivery.Plan{}, delivery.OperationSpec{}, err
	}
	in.RequireOwnerTeam = caller.Policy.RequireOwnerTeam
	plan := delivery.PlanFor(ctx, in)
	if spec.Facet == delivery.FacetPointer && spec.Params.To != "" {
		plan.Precedent = d.precedent(ctx, stage, spec.Params.To)
	}
	return plan, spec, nil
}

// planSources is delivery.PlanSources over the client's own reads, so a plan
// gathers its input exactly as the promoter does.
type planSources struct{ d *Delivery }

func (p planSources) Revisions(ctx context.Context, stage delivery.StageObject, from, to string) (delivery.Revision, delivery.Revision) {
	repo, reg, err := p.d.applyRegistry(ctx, &stage)
	if err != nil {
		return delivery.Revision{Digest: from, Err: err.Error()}, delivery.Revision{Digest: to, Err: err.Error()}
	}
	return delivery.ResolveRevision(ctx, reg, repo, from), delivery.ResolveRevision(ctx, reg, repo, to)
}

func (p planSources) Pactos(ctx context.Context) ([]delivery.PactoView, error) {
	return p.d.pactos(ctx)
}

func (p planSources) Stages(ctx context.Context) ([]delivery.StageObject, error) {
	return p.d.Stages(ctx, "")
}

func (p planSources) Kustomizations(ctx context.Context) ([]delivery.KustomizationView, error) {
	return p.d.kustomizations(ctx)
}

func (p planSources) Describe(ctx context.Context, stage delivery.StageObject, digest string) delivery.Revision {
	return p.d.Describe(ctx, &stage, digest)
}

// Upstream reads the status of the Stage this one takes its candidates from,
// as the promoter does: nil with why when it does not exist or may not be
// read, which the published gate refuses, and any other read error returned,
// so the plan fails instead of predicting without it. An upstream in another
// cluster is read from its signed records, and only with a trust config:
// unverified history never admits a version, and neither does a stream cut
// before the newest record the Stage read.
func (p planSources) Upstream(ctx context.Context, stage delivery.StageObject) (*delivery.StageStatus, string, error) {
	d := p.d
	if r := stage.Spec.Candidates.UpstreamRecords; r != nil {
		if d.Records == nil || d.Trust == nil {
			return nil, "no trust config was given (pass --trust with the key of the upstream recorder), so its records are not read", nil
		}
		repo, err := d.Records(r.Repository)
		if err != nil {
			return nil, err.Error(), nil
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		s, why := deliveryrecord.Upstream(ctx, repo, r.Stream, d.verifier(), stage.Status.UpstreamSeen)
		return s, why, nil
	}
	up := stage.Spec.Candidates.UpstreamStage
	if up == nil {
		return nil, "", nil
	}
	ns := cmp.Or(up.Namespace, stage.Metadata.Namespace)
	s, err := d.Stage(ctx, ns, up.Name)
	switch {
	case apierrors.IsNotFound(err) || apierrors.IsForbidden(err):
		return nil, "the upstream Stage " + ns + "/" + up.Name + " cannot be read: " + err.Error(), nil
	case err != nil:
		return nil, "", err
	}
	return &s.Status, "", nil
}

// spec turns a request into the intent the promoter will admit.
func (d *Delivery) spec(ctx context.Context, req delivery.Request, stage *delivery.StageObject, caller Caller) (delivery.OperationSpec, error) {
	typ := req.Type
	if typ == "" {
		typ = delivery.TypePromote
	}
	t, ok := delivery.TypeByID(typ)
	if !ok {
		return delivery.OperationSpec{}, fmt.Errorf("unknown operation type %q; see `pacto delivery types`", typ)
	}
	spec := delivery.OperationSpec{Type: typ, Stage: req.Stage, Facet: t.Facet, Reason: req.Reason,
		Actor: delivery.Actor{User: caller.User, Groups: caller.Groups}, Cause: req.Cause}
	if req.OnBehalfOf != "" {
		spec.OnBehalfOf = &delivery.Principal{User: req.OnBehalfOf}
	}
	switch {
	case typ == delivery.TypeRollback:
		cited, err := d.Get(ctx, req.Namespace, req.Undoes)
		if err != nil {
			return delivery.OperationSpec{}, fmt.Errorf("a rollback undoes one operation: %w", err)
		}
		spec.Params = delivery.Params{From: cited.Status.Pointer.After, To: cited.Status.Pointer.Before, Undoes: cited.Metadata.UID}
	case typ == delivery.TypeFreeze:
		spec.Params = delivery.Params{From: stage.Status.Pointer}
	case t.Facet == delivery.FacetPointer:
		to, err := d.digest(ctx, stage, req.To)
		if err != nil {
			return delivery.OperationSpec{}, err
		}
		spec.Params = delivery.Params{From: stage.Status.Pointer, To: to}
		if req.From != "" {
			spec.Params.From = req.From
		}
	}
	spec.IntentDigest = delivery.IntentDigest(spec, stage.Spec)
	return spec, nil
}

// digest validates to as a manifest digest. A tag is refused, and the refusal
// names the digest the tag resolves to right now.
func (d *Delivery) digest(ctx context.Context, stage *delivery.StageObject, to string) (string, error) {
	if _, err := delivery.ParseDigest(to); err == nil {
		return to, nil
	}
	if to == "" {
		return "", errors.New("--to is required: the sha256 digest of the artifact to move to")
	}
	repo, reg, err := d.applyRegistry(ctx, stage)
	if err != nil {
		return "", fmt.Errorf("%q is not a digest, and the tag could not be resolved: %w", to, err)
	}
	bounded, cancel := delivery.WithRegistryTimeout(ctx)
	defer cancel()
	resolved, err := reg.Resolve(bounded, repo+":"+strings.TrimPrefix(to, ":"))
	if err != nil {
		return "", fmt.Errorf("%q is not a digest, and the tag could not be resolved: %w", to, err)
	}
	return "", fmt.Errorf("tags are refused, delivery moves digests only: %s currently resolves to %s", to, resolved)
}

// applyRegistry reads the Stage's OCIRepository: its repository and a registry
// client that honours spec.insecure.
func (d *Delivery) applyRegistry(ctx context.Context, stage *delivery.StageObject) (string, DeliveryRegistry, error) {
	repo, insecure, err := d.applyRepo(ctx, stage)
	if err != nil {
		return "", nil, err
	}
	return repo, d.Registry(insecure...), nil
}

// applyRepo reads the Stage's OCIRepository: its repository, and its host
// when spec.insecure says to reach it over plain HTTP.
func (d *Delivery) applyRepo(ctx context.Context, stage *delivery.StageObject) (string, []string, error) {
	u, err := d.Dynamic.Resource(gvrOCIRepository).Namespace(stage.Metadata.Namespace).Get(ctx, stage.Spec.ApplyRef.Name, metav1.GetOptions{})
	if err != nil {
		return "", nil, fmt.Errorf("reading the apply OCIRepository %s: %w", stage.Spec.ApplyRef.Name, err)
	}
	url, _, _ := unstructured.NestedString(u.Object, "spec", "url")
	insecure, _, _ := unstructured.NestedBool(u.Object, "spec", "insecure")
	repo := delivery.RepoFromURL(url)
	if insecure {
		host, _, _ := strings.Cut(repo, "/")
		return repo, []string{host}, nil
	}
	return repo, nil, nil
}

func (d *Delivery) pactos(ctx context.Context) ([]delivery.PactoView, error) {
	list, err := d.Dynamic.Resource(gvrPactos).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	b, _ := list.MarshalJSON()
	return delivery.DecodePactos(b)
}

// kustomizations lists every Flux Kustomization, which places the consumers:
// where each Stage's version runs.
func (d *Delivery) kustomizations(ctx context.Context) ([]delivery.KustomizationView, error) {
	list, err := d.Dynamic.Resource(gvrKustomization).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	b, _ := list.MarshalJSON()
	return delivery.DecodeKustomizations(b)
}

// Propose plans the request and creates the Operation. With an intent digest
// from an earlier plan, it refuses with a *PlanStaleError if anything moved. A
// plan predicted to be rejected is still proposed: the promoter's refusal is a
// decision too, and it leaves a record.
func (d *Delivery) Propose(ctx context.Context, req delivery.Request) (*delivery.OperationObject, delivery.Plan, error) {
	plan, spec, err := d.Plan(ctx, req)
	if err != nil {
		return nil, plan, err
	}
	if req.IntentDigest != "" && req.IntentDigest != plan.IntentDigest {
		return nil, plan, &PlanStaleError{Plan: plan}
	}
	meta := map[string]any{"namespace": req.Namespace, "finalizers": []any{recordFinalizer},
		"labels": map[string]any{"delivery.pacto.trianalab.io/stage": req.Stage}}
	if req.Name != "" {
		meta["name"] = req.Name
	} else {
		meta["generateName"] = req.Stage + "-" + strings.TrimPrefix(spec.Type, "delivery.pacto.trianalab.io/") + "-"
	}
	specJSON, _ := json.Marshal(spec)
	var specMap map[string]any
	_ = json.Unmarshal(specJSON, &specMap)
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "delivery.pacto.trianalab.io/v1alpha1", "kind": "Operation", "metadata": meta, "spec": specMap,
	}}
	created, err := d.Dynamic.Resource(gvrOperations).Namespace(req.Namespace).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		return nil, plan, fmt.Errorf("creating the Operation: %w", err)
	}
	op := &delivery.OperationObject{}
	b, _ := created.MarshalJSON()
	_ = json.Unmarshal(b, op)
	return op, plan, nil
}

// Cancel deletes an Operation. The promoter concludes it Cancelled if nothing
// moved yet. Admission refuses once the pointer moved, and refuses an agent
// cancelling an operation it did not propose itself.
func (d *Delivery) Cancel(ctx context.Context, ns, name string) error {
	if err := d.Dynamic.Resource(gvrOperations).Namespace(ns).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		return fmt.Errorf("cancelling Operation %s/%s: %w", ns, name, err)
	}
	return nil
}

// Approve records a person's decision on one operation, bound to its uid and
// intent digest. Only a person with a fresh login may: admission refuses agents
// and the promoter refuses a login older than 15 minutes.
func (d *Delivery) Approve(ctx context.Context, ns, name string, decision delivery.Decision, note string) (string, error) {
	op, err := d.Get(ctx, ns, name)
	if err != nil {
		return "", err
	}
	caller, err := d.WhoAmI(ctx)
	if err != nil {
		return "", err
	}
	if caller.Class == delivery.ClassAgent {
		return "", errors.New("agents may not approve: Pacto delivery asks a person, and there is no approve tool for agents")
	}
	if caller.AuthTime == "" {
		return "", errors.New("approving needs a fresh interactive login, and the apiserver reports no login time " +
			"(userInfo.extra[" + authTimeExtra + "]); log in again through your identity provider")
	}
	spec := delivery.ApprovalSpec{Operation: name, OperationUID: op.Metadata.UID, IntentDigest: op.Spec.IntentDigest,
		Decision: decision, Note: note, By: caller.User, AuthTime: caller.AuthTime, Groups: caller.Groups}
	freshness := delivery.AuthFreshness
	if st, err := d.Stage(ctx, ns, op.Spec.Stage); err == nil {
		freshness = delivery.PublishedFreshness(st.Status.AuthFreshness)
	}
	if ok, why := delivery.ApprovalEligible(op.Spec, op.Status, op.Metadata.UID, spec, d.Now(), caller.Policy, freshness); !ok {
		return "", errors.New("the promoter would not count this decision, so none was recorded: " + why)
	}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "delivery.pacto.trianalab.io/v1alpha1", "kind": "Approval",
		"metadata": map[string]any{"namespace": ns, "generateName": name + "-",
			"ownerReferences": []any{map[string]any{"apiVersion": "delivery.pacto.trianalab.io/v1alpha1", "kind": "Operation",
				"name": name, "uid": op.Metadata.UID}}},
		"spec": map[string]any{"operation": name, "operationUID": op.Metadata.UID, "intentDigest": op.Spec.IntentDigest,
			"decision": string(decision), "note": note, "by": caller.User, "authTime": caller.AuthTime, "groups": anySlice(caller.Groups)},
	}}
	created, err := d.Dynamic.Resource(gvrApprovals).Namespace(ns).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("creating the Approval: %w", err)
	}
	return created.GetName(), nil
}
