package app

import (
	"context"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// RegressionEvidence reads what delivery recorded against the regressed
// revision of ev.Service, with this client's own identity: the Stages that
// list it as failed, their re-check failures and the rollbacks off it. What
// could not be read is named in Unavailable, never taken as "no failure".
func (d *Delivery) RegressionEvidence(ctx context.Context, ev impact.Event) *impact.Regression {
	if !strings.HasPrefix(ev.Revision, "sha256:") {
		return &impact.Regression{Unavailable: "name the artifact digest (sha256:...) to read delivery evidence: a version does not identify one artifact"}
	}
	ctx, cancel := context.WithTimeout(ctx, auditTimeout)
	defer cancel()
	domain, name := fleet.ParseServiceKey(fleet.ServiceKey(ev.Service))
	w, err := d.auditStages(ctx, "", delivery.ServiceRef{Domain: domain, Name: name})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return &impact.Regression{Unavailable: "this cluster does not serve delivery.pacto.trianalab.io: " + err.Error()}
		}
		return &impact.Regression{Unavailable: "delivery evidence could not be read: " + err.Error()}
	}
	// Every Stage's history is read: a person's rollback off the digest
	// leaves failedDigests unchanged, so the list cannot narrow the read.
	hs := d.Histories(ctx, w.Stages)
	evidence, unknown := delivery.RegressionEvidence(w.Stages, hs.Histories, ev.Revision)
	var why []string
	if len(w.Forbidden) > 0 {
		why = append(why, "evidence from these namespaces was not read: "+strings.Join(w.Forbidden, "; "))
	}
	if len(hs.Unreadable) > 0 {
		why = append(why, "the histories of these Stages were not read: "+strings.Join(hs.Unreadable, "; "))
	}
	why = append(why, unknown...)
	return &impact.Regression{Evidence: evidence, Unavailable: strings.Join(why, "; ")}
}
