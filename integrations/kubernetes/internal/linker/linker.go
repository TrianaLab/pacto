/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package linker

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	pactov1 "github.com/trianalab/pacto/integrations/kubernetes/v7/api/v1"
	"github.com/trianalab/pacto/integrations/kubernetes/v7/internal/component"
	"github.com/trianalab/pacto/integrations/kubernetes/v7/internal/detection"
	"github.com/trianalab/pacto/integrations/kubernetes/v7/internal/observer"
	core "github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// linkInterval is how often the linker links and evaluates.
// ponytail: fixed; a value if a large cluster needs a slower one.
const linkInterval = time.Minute

// pageSize is the List page of every read.
const pageSize = 500

// The linker's limitation codes.
const (
	// LimitationIndexPartial: some index entries or images records were not
	// read this cycle, so no new workload is linked by digest until they are.
	LimitationIndexPartial = "LINK_INDEX_PARTIAL"
	// LimitationTargetsCapped: more linked workloads than linked.json holds.
	LimitationTargetsCapped = "LINKED_TARGETS_CAPPED"
	// LimitationAnnotationUnreadable: the contract an annotation names could
	// not be loaded, so the workload is unknown.
	LimitationAnnotationUnreadable = "LINK_ANNOTATION_UNREADABLE"
)

// Linker is the leader-only runnable that links every workload no Pacto
// targets to the published contract it runs, judges each through Evaluate on
// a Pacto that is never stored, and writes the result as the linked.json key
// of the detection ConfigMap.
type Linker struct {
	// Reader must be uncached: the linker adds no informer to the manager.
	Reader client.Reader
	Writer client.Client
	// Evaluate judges a transient Pacto with the operator's credentials;
	// EvaluateAnonymous with none, for an annotation naming a repository the
	// organization index does not hold.
	Evaluate, EvaluateAnonymous func(context.Context, *pactov1.Pacto)
	Index                       *Index
	// Tags lists the tags of <registry>/pacto-index and Registry reads its
	// entries and the images records; both nil without a registry, which
	// turns the digest signal off.
	Tags     func(context.Context) ([]string, error)
	Registry Reader
	// Namespace and ConfigMap name the detection ConfigMap the Deployment
	// DeploymentName owns; Cluster is the kube-system UID ("" unknown).
	Namespace, ConfigMap, DeploymentName, Cluster string
	// Watched are the namespaces of a scoped operator; empty reads all.
	Watched []string
	Now     func() time.Time

	owner   *metav1ac.OwnerReferenceApplyConfiguration
	seeded  bool
	since   map[sinceKey]time.Time
	windows map[Key][]pactov1.ObservationWindow
	last    atomic.Pointer[cycle]
	// maxBytes overrides core.MaxLinkedBytes and maxPods detect.MaxPods
	// (tests).
	maxBytes, maxPods int
}

// sinceKey is a workload linked to a service.
type sinceKey struct {
	Key
	service string
}

// cycle is one Sync's result.
type cycle struct {
	linked   core.LinkedTargets
	siblings []observer.LinkedSibling
}

// Start implements manager.Runnable. Runnables default to leader election,
// so only the leader writes.
func (l *Linker) Start(ctx context.Context) error {
	return component.Run(ctx, "linker", true, linkInterval, l.Sync)
}

// Current is the last cycle's linked targets; zero (unknown) before the
// first.
func (l *Linker) Current() core.LinkedTargets {
	if c := l.last.Load(); c != nil {
		return c.linked
	}
	return core.LinkedTargets{}
}

// Siblings are the last cycle's linked workloads, for dependency resolution.
func (l *Linker) Siblings() []observer.LinkedSibling {
	if c := l.last.Load(); c != nil {
		return c.siblings
	}
	return nil
}

// Sync links once and writes linked.json. It fails only on its own
// misconfiguration: a read or write failure is a partial limitation or a
// log line, since an advisory feature never stops the operator.
func (l *Linker) Sync(ctx context.Context) error {
	if l.Reader == nil || l.Writer == nil || l.ConfigMap == "" || l.Evaluate == nil || l.EvaluateAnonymous == nil || l.Index == nil {
		return errors.New("the linker needs a reader, a writer, its ConfigMap, an index and both evaluators")
	}
	log := logf.FromContext(ctx).WithName("linker")
	if l.owner == nil {
		owner, err := detection.OwnerRef(ctx, l.Reader, l.Namespace, l.DeploymentName)
		if err != nil {
			log.Info("The linker waits for the operator Deployment to own its ConfigMap", "deployment", l.DeploymentName)
			return nil
		}
		l.owner = owner
	}
	cm := &corev1.ConfigMap{}
	err := l.Reader.Get(ctx, client.ObjectKey{Namespace: l.Namespace, Name: l.ConfigMap}, cm) // absent: nothing to seed or share
	if !l.seeded {
		if err != nil && !apierrors.IsNotFound(err) {
			// Writing now would replace every Since a previous linker wrote.
			log.Info("The linker waits to read the linked workloads it wrote before", "configMap", l.ConfigMap)
			return nil
		}
		l.seeded = true
		l.seed(cm)
	}
	c := l.link(ctx)
	data := l.encode(&c.linked, room(cm))
	l.last.Store(c)
	err = l.Writer.Apply(ctx, corev1ac.ConfigMap(l.ConfigMap, l.Namespace).
		WithLabels(map[string]string{
			"app.kubernetes.io/part-of":   "pacto",
			"app.kubernetes.io/component": "detection",
			detect.ManagedByLabel:         detect.ManagedBy,
		}).
		WithOwnerReferences(l.owner).
		WithData(map[string]string{core.LinkedConfigMapKey: string(data)}),
		client.FieldOwner(core.LinkerFieldManager), client.ForceOwnership)
	if err != nil {
		log.Error(err, "Writing the linked workloads failed", "configMap", l.ConfigMap)
	}
	return nil
}

// seed takes Since from the linked.json a previous linker wrote, so a
// restart records no new link; only the observation windows start over.
func (l *Linker) seed(cm *corev1.ConfigMap) {
	prev, err := core.DecodeLinked([]byte(cm.Data[core.LinkedConfigMapKey]))
	if err != nil {
		return
	}
	l.since = map[sinceKey]time.Time{}
	for _, v := range prev.Targets {
		kind, name, _ := strings.Cut(v.Metadata.Name, "/")
		at, err := time.Parse(time.RFC3339, v.Metadata.Labels[core.LinkedSinceLabel])
		if err == nil && v.Status.Contract != nil {
			l.since[sinceKey{Key{v.Metadata.Namespace, kind, name}, v.Status.Contract.ServiceName}] = at
		}
	}
}

// world is what one cycle read.
type world struct {
	workloads []Workload
	services  map[string][]corev1.Service
	lims      []fleet.Limitation
}

// link reads the cluster and the index, matches every workload and judges
// each link.
func (l *Linker) link(ctx context.Context) *cycle {
	now := l.Now().UTC()
	w := l.read(ctx)
	out := core.LinkedTargets{SchemaVersion: core.LinkedSchemaVersion, Cluster: l.Cluster, AsOf: now.Format(time.RFC3339),
		Completeness: fleet.CompletenessComplete, Limitations: w.lims, Targets: []core.PactoView{}}
	byDigest, lims := l.candidates(ctx, w.workloads)
	out.Limitations = append(out.Limitations, lims...)
	poolPartial := slices.ContainsFunc(lims, func(lim fleet.Limitation) bool { return lim.Code == LimitationIndexPartial })
	c := &cycle{}
	since, windows := map[sinceKey]time.Time{}, map[Key][]pactov1.ObservationWindow{}
	for _, wk := range w.workloads {
		link, lim := Match(wk, byDigest)
		if lim.Code != "" {
			out.Limitations = append(out.Limitations, lim)
		}
		key := Key{wk.Namespace, wk.Kind, wk.Name}
		if link.Signal == fleet.LinkSignalImageDigest && poolPartial && !l.linkedBefore(key) {
			// An unread entry may hold another service or version recording
			// the digest: a partial pool makes no new digest link, so an
			// ambiguity never becomes a merge.
			continue
		}
		if link.Signal == "" {
			continue
		}
		p := l.judge(ctx, wk, link, ServiceFor(w.services[wk.Namespace], wk.Labels))
		windows[key] = p.Status.ObservationWindows
		if lim := l.refuse(wk, link, p); lim.Code != "" {
			out.Limitations = append(out.Limitations, lim)
			continue
		}
		sk := sinceKey{key, p.Status.Contract.ServiceName}
		since[sk] = cmp.Or(l.since[sk], now)
		at := since[sk]
		out.Targets = append(out.Targets, view(p, wk, fleet.ObservedLink{Signal: link.Signal, Value: link.Value, Since: &at, OtherVersions: link.OtherVersions}))
		version := p.Status.Contract.Version
		if len(link.OtherVersions) > 0 {
			version = "" // a guess: a dependency on it reads no version
		}
		c.siblings = append(c.siblings, observer.LinkedSibling{Namespace: wk.Namespace, ServiceName: p.Spec.Target.ServiceName,
			ResolvedRef: p.Status.Contract.ResolvedRef, Version: version})
	}
	slices.SortFunc(out.Targets, func(a, b core.PactoView) int {
		return cmp.Or(cmp.Compare(a.Metadata.Namespace, b.Metadata.Namespace), cmp.Compare(a.Metadata.Name, b.Metadata.Name))
	})
	for _, lim := range out.Limitations {
		if lim.Code == fleet.LimitationSourcePartial || lim.Code == LimitationIndexPartial || lim.Code == LimitationAnnotationUnreadable {
			out.Completeness = fleet.CompletenessPartial
		}
	}
	if out.Completeness == fleet.CompletenessPartial {
		l.carry(since, windows)
	}
	l.since, l.windows = since, windows
	c.linked = out
	return c
}

// linkedBefore says the previous cycle (or the linked.json a restart seeded)
// linked key to some service.
func (l *Linker) linkedBefore(key Key) bool {
	for sk := range l.since {
		if sk.Key == key {
			return true
		}
	}
	return false
}

// carry keeps, after a partial cycle, the Since and windows of every workload
// this cycle did not link: a failed listing, a cut index read or a contract
// that did not load is no evidence the link ended, so the next good cycle
// records no new link for it. A complete cycle drops them.
// ponytail: kept while cycles stay partial; a TTL if a scope fails for good.
func (l *Linker) carry(since map[sinceKey]time.Time, windows map[Key][]pactov1.ObservationWindow) {
	linked := map[Key]bool{}
	for sk := range since {
		linked[sk.Key] = true
	}
	for sk, at := range l.since {
		if !linked[sk.Key] {
			since[sk] = at
		}
	}
	for k, w := range l.windows {
		if _, ok := windows[k]; !ok {
			windows[k] = w
		}
	}
}

// judge evaluates wk against link's contract on a Pacto that is never
// stored, with the observation windows of the previous cycle. An annotation
// naming a repository outside the organization index is loaded anonymously.
func (l *Linker) judge(ctx context.Context, wk Workload, link Link, service string) *pactov1.Pacto {
	p := &pactov1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Namespace: wk.Namespace, Name: wk.Name},
		Spec: pactov1.PactoSpec{
			ContractRef: pactov1.ContractRef{OCI: link.Ref},
			Target:      pactov1.TargetRef{ServiceName: service, WorkloadRef: &pactov1.WorkloadRef{Kind: wk.Kind, Name: wk.Name}},
		},
		Status: pactov1.PactoStatus{ObservationWindows: l.windows[Key{wk.Namespace, wk.Kind, wk.Name}]},
	}
	evaluate := l.Evaluate
	if link.Signal == fleet.LinkSignalAnnotation && !l.Index.Indexed(refRepository(link.Ref)) {
		evaluate = l.EvaluateAnonymous
	}
	evaluate(ctx, p)
	return p
}

// refuse is why a judged link is not written: its contract did not load, or
// the bundle a digest linked to speaks for another service than its index
// entry claims.
func (l *Linker) refuse(wk Workload, link Link, p *pactov1.Pacto) fleet.Limitation {
	c := p.Status.Contract
	switch {
	case c == nil && link.Signal == fleet.LinkSignalAnnotation:
		return fleet.Limitation{Code: LimitationAnnotationUnreadable, Message: caller(wk) + " names a contract in its " +
			AnnotationContract + " annotation that could not be loaded, so whether it complies is unknown"}
	case c == nil:
		return fleet.Limitation{Code: fleet.LimitationSourcePartial, Message: "the contract " + caller(wk) +
			" runs could not be loaded, so whether it complies is unknown"}
	}
	if claimed, ok := l.Index.claimed(link.Ref); ok && link.Signal == fleet.LinkSignalImageDigest && claimed != c.ServiceName {
		return fleet.Limitation{Code: fleet.LimitationInferredReplacementAmbiguous, Message: fmt.Sprintf(
			"%s runs an image the index names %s for, but its bundle is %s, so it links to none", caller(wk), claimed, c.ServiceName)}
	}
	return fleet.Limitation{}
}

// view is the linked target of p: a Pacto view named <Kind>/<name> whose
// link rides in its labels.
func view(p *pactov1.Pacto, wk Workload, link fleet.ObservedLink) core.PactoView {
	var v core.PactoView
	b, _ := json.Marshal(p) // plain data
	_ = json.Unmarshal(b, &v)
	v.Metadata.Name = wk.Kind + "/" + wk.Name
	v.Metadata.Labels = core.LinkLabels(link)
	return v
}

// maxConfigMapBytes is the API server's bound on a ConfigMap's data, keys
// included; detectionHeadroom is what the linker leaves detection to grow
// into between two cycles.
const (
	maxConfigMapBytes = 1 << 20
	detectionHeadroom = 64 << 10
)

// room is the most linked.json may take in cm beside the detection payload
// and its headroom, and the full bound of clusters.json whatever its size
// now (it can grow to its bound between two cycles), so the keys together
// stay under the ConfigMap bound.
func room(cm *corev1.ConfigMap) int {
	return maxConfigMapBytes - detectionHeadroom - len(detect.ConfigMapKey) - len(cm.Data[detect.ConfigMapKey]) - len(core.LinkedConfigMapKey) -
		len(detect.ClustersConfigMapKey) - detect.MaxClustersBytes
}

// encode encodes out within the smaller of the bound and room, dropping the
// last targets (sorted by namespace, kind and name) until it fits; with no
// target left it returns what it has, and the write reports the failure.
// ponytail: one ConfigMap per cluster; one per namespace when a cluster links more than about 200 workloads.
func (l *Linker) encode(out *core.LinkedTargets, room int) []byte {
	limit := min(cmp.Or(l.maxBytes, core.MaxLinkedBytes), room)
	total := len(out.Targets)
	for {
		b, err := core.EncodeLinked(*out)
		if err == nil && len(b) <= limit || len(out.Targets) == 0 {
			return b
		}
		if len(out.Targets) == total {
			out.Completeness = fleet.CompletenessPartial
			out.Limitations = append(out.Limitations, fleet.Limitation{Code: LimitationTargetsCapped})
		}
		out.Targets = out.Targets[:len(out.Targets)-1]
		out.Limitations[len(out.Limitations)-1].Message = fmt.Sprintf(
			"kept %d of %d linked workloads, ordered by namespace, kind and name, within the %d-byte bound", len(out.Targets), total, limit)
	}
}

// candidates are the digest candidates of the organization index, and why
// they may be incomplete. Without a registry the digest signal is off.
func (l *Linker) candidates(ctx context.Context, ws []Workload) (map[string][]Candidate, []fleet.Limitation) {
	if l.Tags == nil {
		return nil, []fleet.Limitation{{Code: core.LimitationLinkDigestOff,
			Message: "the operator has no registry, so only workloads annotated with " + AnnotationContract + " are linked"}}
	}
	tags, err := l.Tags(ctx)
	if err != nil {
		return nil, []fleet.Limitation{{Code: LimitationIndexPartial, Message: "the organization index could not be listed, so no workload is linked by image digest"}}
	}
	var imageTags []string
	for _, w := range ws {
		for _, img := range w.Images {
			if img.Tag != "" {
				imageTags = append(imageTags, img.Tag)
			}
		}
	}
	byDigest, unread, partial := l.Index.Candidates(ctx, tags, imageTags, l.Registry)
	if !partial {
		return byDigest, nil
	}
	return byDigest, []fleet.Limitation{{Code: LimitationIndexPartial, Message: fmt.Sprintf(
		"%d index reads are left for the next cycle and some may have failed, so no new workload is linked by image digest until they are read", unread)}}
}

// read lists, in each scope, the Pactos, the workloads the observer can
// judge (Deployments, StatefulSets and CronJobs), the ReplicaSets and Jobs
// that own their pods, the running pods and the Services. A list that fails
// leaves that scope partial.
func (l *Linker) read(ctx context.Context) world {
	w := world{services: map[string][]corev1.Service{}}
	scopes := l.Watched
	if len(scopes) == 0 {
		scopes = []string{""}
	}
	for _, ns := range scopes {
		if err := l.readScope(ctx, ns, &w); err != nil {
			w.lims = append(w.lims, fleet.Limitation{Code: fleet.LimitationSourcePartial,
				Message: "the operator could not read the workloads of " + cmp.Or(ns, "the cluster") + ", so they are not linked"})
		}
	}
	return w
}

// readScope adds the workloads and Services of namespace ns ("" all) to w.
func (l *Linker) readScope(ctx context.Context, ns string, w *world) error {
	var (
		pactos pactov1.PactoList
		deps   appsv1.DeploymentList
		sts    appsv1.StatefulSetList
		crons  batchv1.CronJobList
		rss    appsv1.ReplicaSetList
		jobs   batchv1.JobList
		svcs   corev1.ServiceList
	)
	for _, list := range []client.ObjectList{&pactos, &deps, &sts, &crons, &rss, &jobs, &svcs} {
		if err := l.listAll(ctx, list, ns); err != nil {
			return err
		}
	}
	pods, sampled, err := l.pods(ctx, ns)
	if err != nil {
		return err
	}
	if sampled {
		w.lims = append(w.lims, fleet.Limitation{Code: fleet.LimitationSourcePartial, Message: fmt.Sprintf(
			"the operator sampled the first %d running pods of %s, so a workload whose pods it did not read may be unlinked or linked by part of its images", cmp.Or(l.maxPods, detect.MaxPods), cmp.Or(ns, "the cluster"))})
	}
	targeted := map[Key]bool{}
	for i := range pactos.Items {
		name, kind := pactos.Items[i].ResolvedWorkload()
		targeted[Key{pactos.Items[i].Namespace, kind, name}] = true
	}
	digests := Digests(pods, owners(rss.Items, jobs.Items))
	add := func(kind string, m metav1.ObjectMeta, labels map[string]string) {
		k := Key{m.Namespace, kind, m.Name}
		w.workloads = append(w.workloads, Workload{Namespace: m.Namespace, Kind: kind, Name: m.Name,
			Annotation: m.Annotations[AnnotationContract], Labels: labels, Images: digests[k], Targeted: targeted[k]})
	}
	for _, d := range deps.Items {
		add("Deployment", d.ObjectMeta, d.Spec.Template.Labels)
	}
	for _, s := range sts.Items {
		add("StatefulSet", s.ObjectMeta, s.Spec.Template.Labels)
	}
	for _, c := range crons.Items {
		add("CronJob", c.ObjectMeta, c.Spec.JobTemplate.Spec.Template.Labels)
	}
	for _, s := range svcs.Items {
		w.services[s.Namespace] = append(w.services[s.Namespace], s)
	}
	return nil
}

// owners maps each ReplicaSet a Deployment controls, and each Job a CronJob
// controls, to that owner.
func owners(rss []appsv1.ReplicaSet, jobs []batchv1.Job) map[types.UID]Owner {
	out := map[types.UID]Owner{}
	for i := range rss {
		if c := metav1.GetControllerOf(&rss[i]); c != nil && c.Kind == "Deployment" {
			out[rss[i].UID] = Owner{Kind: c.Kind, Name: c.Name}
		}
	}
	for i := range jobs {
		if c := metav1.GetControllerOf(&jobs[i]); c != nil && c.Kind == "CronJob" {
			out[jobs[i].UID] = Owner{Kind: c.Kind, Name: c.Name}
		}
	}
	return out
}

// pods reads the running pods of ns, at most maxPods; sampled says it
// stopped there with pods left unread.
func (l *Linker) pods(ctx context.Context, ns string) (out []corev1.Pod, sampled bool, err error) {
	limit := cmp.Or(l.maxPods, detect.MaxPods)
	next := ""
	for {
		page := &corev1.PodList{}
		if err := l.Reader.List(ctx, page, client.InNamespace(ns), client.Limit(pageSize), client.Continue(next)); err != nil {
			return nil, false, err
		}
		for _, p := range page.Items {
			if p.Status.Phase != corev1.PodRunning {
				continue
			}
			if len(out) == limit {
				return out, true, nil
			}
			out = append(out, p)
		}
		if next = page.Continue; next == "" {
			return out, false, nil
		}
	}
}

// listAll fills list with every object of its kind in ns, page by page.
// Each page decodes into a fresh list: the decoder reuses the items of the
// list it fills, so a reused one would overwrite the previous page's objects.
func (l *Linker) listAll(ctx context.Context, list client.ObjectList, ns string) error {
	empty := list.DeepCopyObject().(client.ObjectList) // list is empty here
	var items []runtime.Object
	next := ""
	for {
		page := empty.DeepCopyObject().(client.ObjectList)
		if err := l.Reader.List(ctx, page, client.InNamespace(ns), client.Limit(pageSize), client.Continue(next)); err != nil {
			return err
		}
		objs, _ := apimeta.ExtractList(page) // every list here is a typed list
		items = append(items, objs...)
		if next = page.GetContinue(); next == "" {
			return apimeta.SetList(list, items)
		}
	}
}

// refRepository is the repository of an oci:// reference, tag and digest
// removed.
func refRepository(ref string) string {
	rest := strings.TrimPrefix(ref, "oci://")
	rest, _, _ = strings.Cut(rest, "@")
	if i := strings.LastIndex(rest, ":"); i > strings.LastIndex(rest, "/") {
		rest = rest[:i]
	}
	return rest
}
