/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// signedApproval is an approval record a person signed keyless, verified:
// who the certificate names, as whom it is checked, and when a timestamp
// authority or the log saw it signed.
type signedApproval struct {
	st       delivery.ApprovalStatement
	id       string
	identity delivery.Identity
	source   delivery.ClassSource
	at       time.Time
}

// openSigned opens bundle, an approval record, through
// signing.OpenPersonApproval: the recorder and the promoter both go through
// it, so a record becomes a decision in one place.
func openSigned(bundle []byte, p delivery.ClassPolicy) (signedApproval, error) {
	a, err := signing.OpenPersonApproval(bundle, p)
	return signedApproval{st: a.Statement, id: a.ID, identity: a.Identity, source: a.Source, at: a.At}, err
}

// verifySigned fetches and verifies the keyless record the view a names, and
// says why a does not count when the record does not say what a says. The
// promoter checks the record itself: only the recorder writes spec.record,
// and the promoter trusts no writer for what a signature can show.
// ponytail: fetched again on every poll until the view counts; cache by
// manifest digest if many views wait at once.
func (r *OperationReconciler) verifySigned(ctx context.Context, s delivery.ApprovalSpec) (signedApproval, string) {
	path, manifest, _ := strings.Cut(s.Record, "@")
	ctx, cancel := delivery.WithRegistryTimeout(ctx)
	defer cancel()
	repo, err := ocireferrer.Open(path, r.RecordOptions)
	var bundle []byte
	if err == nil {
		bundle, err = deliveryrecord.Fetch(ctx, repo, manifest, delivery.PredicateApproval)
	}
	if err != nil {
		return signedApproval{}, "its signed record " + s.Record + " cannot be read: " + err.Error()
	}
	signed, err := openSigned(bundle, r.Policy)
	if err != nil {
		return signedApproval{}, "its signed record does not verify: " + err.Error()
	}
	p := signed.st.Predicate
	if code, why := delivery.KeylessRefusal(signed.identity, signed.source); code != "" {
		return signedApproval{}, why
	}
	switch {
	case signed.identity.User != s.By:
		return signedApproval{}, "its record is signed by " + signed.identity.User + ", not " + s.By
	case ts(signed.at) != s.SignedAt:
		return signedApproval{}, "its signing time is not the one its record was signed at"
	case p.Operation.UID != s.OperationUID || p.Operation.IntentDigest != s.IntentDigest || p.Decision != s.Decision || p.Note != s.Note:
		return signedApproval{}, "it does not say what its signed record says"
	case r.Freshness > 0:
		return signedApproval{}, "this Stage asks for a fresh login, which a signed approval cannot show; approve from the dashboard or with kubectl"
	}
	return signed, ""
}
