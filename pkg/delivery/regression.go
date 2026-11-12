package delivery

import (
	"cmp"
	"slices"
	"strconv"

	"github.com/trianalab/pacto/v4/pkg/impact"
)

// RegressionEvidence is what the Stages and their histories record against
// digest: the Stages that list it as failed, the newest re-check's Fail
// verdicts about it and the rollbacks that moved a Stage off it. unknown names
// the Stages whose failed list is full without it, where an older failure may
// have aged out. The caller passes only the Stages of the regressed service.
//
// A rollback counts only when its pointer left the digest (the left role):
// From alone is set even when nothing moved.
func RegressionEvidence(stages []StageObject, histories []StageHistory, digest string) (ev []impact.RegressionEvidence, unknown []string) {
	read := map[[2]string]bool{}
	for _, st := range stages {
		ns, name := st.Metadata.Namespace, st.Metadata.Name
		read[[2]string{ns, name}] = true
		switch {
		case slices.Contains(st.Status.FailedDigests, digest):
			ev = append(ev, impact.RegressionEvidence{Namespace: ns, Stage: name, Digest: digest, Kind: impact.RegressionFailed,
				Message: "the Stage lists " + digest + " among the versions that failed there"})
		case len(st.Status.FailedDigests) >= MaxFailedDigests:
			unknown = append(unknown, ns+"/"+name+": its failed list is full ("+strconv.Itoa(MaxFailedDigests)+
				" entries), so an older failure of "+digest+" may have aged out")
		}
		if lc := st.Status.LastCheck; lc != nil && lc.Digest == digest {
			for _, v := range lc.Verdicts {
				if v.Result == Fail {
					ev = append(ev, impact.RegressionEvidence{Namespace: ns, Stage: name, Digest: digest, Kind: impact.RegressionCheck,
						Gate: v.Gate, Result: string(v.Result), Message: v.Message, At: lc.At, Operation: lc.Operation})
				}
			}
		}
	}
	for _, h := range histories {
		if !read[[2]string{h.Namespace, h.Stage.Name}] {
			continue
		}
		for _, e := range h.Entries {
			if e.Type == TypeRollback && lifecycleRole(e, digest) == "left" {
				ev = append(ev, rollbackEvidence(h, e, digest))
			}
		}
	}
	slices.SortStableFunc(ev, func(a, b impact.RegressionEvidence) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Stage, b.Stage), cmp.Compare(a.Kind, b.Kind))
	})
	return ev, unknown
}

// rollbackEvidence cites one rollback off digest with its cause.
func rollbackEvidence(h StageHistory, e HistoryEntry, digest string) impact.RegressionEvidence {
	out := impact.RegressionEvidence{Namespace: h.Namespace, Stage: h.Stage.Name, Digest: digest, Kind: impact.RegressionRollback,
		Message: e.Reason, At: e.Finished, Operation: e.Operation, Record: e.Record}
	if c := e.Cause; c != nil {
		if v := c.Verdict; v != nil {
			out.Gate, out.Result, out.Message = v.Gate, string(v.Result), v.Message
		}
		if c.Reason != "" {
			out.Message = c.Reason
		}
	}
	return out
}
