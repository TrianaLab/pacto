package delivery

import (
	"context"
	"strings"
	"sync"
)

// Where a WorkloadAccess answer came from.
const (
	// AccessBySubjectReview: the promoter asked the apiserver a
	// SubjectAccessReview about another identity (an approval's author).
	AccessBySubjectReview = "subject-access-review"
	// AccessBySelfReview: a client asked a SelfSubjectAccessReview with its
	// caller's own credentials.
	AccessBySelfReview = "self-subject-access-review"
)

// WorkloadAccess answers the default approver rule: may an identity update
// the Stage's workload. Only people who may update it decide its held changes. Unknown
// is never allowed: Unverified says why nobody could ask.
type WorkloadAccess struct {
	Resource   string `json:"resource" doc:"The workload's resource, such as deployments"`
	Name       string `json:"name" doc:"The workload's name"`
	Namespace  string `json:"namespace" doc:"The namespace of the Stage and its workload"`
	Allowed    bool   `json:"allowed" doc:"The apiserver allows update on the workload; false when denied or unverified"`
	Unverified string `json:"unverified,omitempty" doc:"Why nobody could ask: the review failed, timed out or the kind has no resource. Never allowed"`
	Source     string `json:"source,omitempty" doc:"subject-access-review (the promoter, about another identity) or self-subject-access-review (a client, about its caller)"`
	At         string `json:"at,omitempty" doc:"When the review was answered (RFC 3339); empty when nobody could ask"`
}

// WorkloadResource is the API group and resource of a workload kind the
// promoter reads; ok is false for any other kind.
func WorkloadResource(kind string) (group, resource string, ok bool) {
	switch kind {
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet":
		return "apps", strings.ToLower(kind) + "s", true
	case "Job", "CronJob":
		return "batch", strings.ToLower(kind) + "s", true
	}
	return "", "", false
}

// AccessFor is the unasked access to the workload of stage in namespace and
// its API group: Unverified when the kind has no resource.
func AccessFor(stage StageSpec, namespace string) (WorkloadAccess, string) {
	w := stage.WorkloadOf()
	group, resource, ok := WorkloadResource(w.Kind)
	a := WorkloadAccess{Resource: resource, Name: w.Name, Namespace: namespace}
	if !ok {
		a.Resource = strings.ToLower(w.Kind)
		a.Unverified = "the workload kind " + w.Kind + " is none Pacto knows how to ask about"
	}
	return a, group
}

// Phrase is the action the access is about: update deployments/checkout in
// payments.
func (a WorkloadAccess) Phrase() string {
	return "update " + a.Resource + "/" + a.Name + " in " + a.Namespace
}

// StageAccess answers the access of the caller to the workload of the Stage
// ns/stage.
type StageAccess func(ctx context.Context, ns, stage string) WorkloadAccess

// AccessMemo asks ask once per namespace, kind and name for the Stages in
// stages: one review per workload in a request, however many operations or
// rows name it. A Stage missing from stages, or of a kind with no resource, is
// unverified and never asked.
// ponytail: the lock is held across the review, so concurrent callers wait
// on one; per-key waiting if many workloads are asked at once.
func AccessMemo(stages []StageObject, ask func(ctx context.Context, ns string, w WorkloadRef) WorkloadAccess) StageAccess {
	byStage := StagesByName(stages)
	var mu sync.Mutex
	seen := map[string]WorkloadAccess{}
	return func(ctx context.Context, ns, stage string) WorkloadAccess {
		st, ok := byStage[ns+"/"+stage]
		if !ok {
			return WorkloadAccess{Namespace: ns, Name: stage, Resource: "stages", Unverified: "the Stage " + stage + " could not be read"}
		}
		a, _ := AccessFor(st.Spec, ns)
		if a.Unverified != "" {
			return a
		}
		w := st.Spec.WorkloadOf()
		key := ns + "/" + w.Kind + "/" + w.Name
		mu.Lock()
		defer mu.Unlock()
		if got, ok := seen[key]; ok {
			return got
		}
		seen[key] = ask(ctx, ns, w)
		return seen[key]
	}
}
