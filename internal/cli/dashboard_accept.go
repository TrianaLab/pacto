package cli

import (
	"cmp"
	"context"
	"errors"
	"strings"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// dashboardAcceptPlan plans "Accept all proposed ranges" for the dashboard:
// pacto accept --proposed --dry-run over the revision's bundle repository,
// read with the dashboard's own registry credentials. It returns the plan and
// the command a person runs to sign it; it never signs and never writes,
// because a dashboard that signed would let any holder of a session mint a
// person's signature (Decision 14).
func dashboardAcceptPlan(svc *app.Service, trust func(context.Context) (app.Installation, error)) func(context.Context, fleet.ServiceKey, *fleet.ContractRevision) (dashboard.AcceptPlanAnswer, error) {
	return func(ctx context.Context, _ fleet.ServiceKey, rev *fleet.ContractRevision) (dashboard.AcceptPlanAnswer, error) {
		repo := revisionRepo(rev)
		if repo == "" {
			return dashboard.AcceptPlanAnswer{Refusal: &dashboard.AcceptRefusal{Code: "NotPublished",
				Message: rev.Contract.Service.Name + " names no bundle repository: declare publish.bundle and publish it first", Command: "pacto ci --publish ."}}, nil
		}
		p, err := svc.AcceptPlan(ctx, app.AcceptOptions{Ref: repo, Proposed: true, Trust: trust})
		var de *app.DeclareError
		if errors.As(err, &de) && (de.Code == "AcceptNothingProposed" || de.Code == "NotPublished") {
			return dashboard.AcceptPlanAnswer{Refusal: &dashboard.AcceptRefusal{Code: de.Code, Message: de.Message, Command: de.CLI}}, nil
		}
		if err != nil {
			return dashboard.AcceptPlanAnswer{}, err
		}
		return dashboard.AcceptPlanAnswer{
			Plan: &dashboard.AcceptPlanView{Service: p.Service, Version: p.Version, Subject: p.Subject, Ranges: p.Ranges,
				WhoMayAccept: p.WhoMayAccept, Existing: p.Existing, Notes: p.Notes, IntentDigest: p.IntentDigest},
			Command: "pacto accept " + contract.OCIScheme + repo + " --proposed --intent " + p.IntentDigest,
		}, nil
	}
}

// revisionRepo is the bundle repository a revision is published to: its
// publish.bundle, else the oci:// reference it was read from; "" for neither.
func revisionRepo(rev *fleet.ContractRevision) string {
	if p := rev.Contract.Publish; p != nil && p.Bundle != "" {
		return repoOf(p.Bundle)
	}
	if ref := cmp.Or(rev.ResolvedRef, rev.RequestedRef); strings.HasPrefix(ref, contract.OCIScheme) {
		return repoOf(ref)
	}
	return ""
}
