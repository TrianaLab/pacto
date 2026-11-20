package signing

import (
	"errors"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// PersonApproval is an approval record a person signed keyless, verified:
// the issuer and subject the certificate names, the identity it speaks for
// (delivery.ApproverIdentity), whether a person, an agent or a CI workload
// signed it (delivery.ClassifySigner) and when a timestamp authority or the
// log saw it signed.
type PersonApproval struct {
	Statement    delivery.ApprovalStatement
	ID           string
	Issuer       string
	SAN          string
	Identity     delivery.Identity
	Source       delivery.ClassSource
	Class        delivery.ActorClass
	ClassifiedBy string
	At           time.Time
}

// OpenPersonApproval opens bundle, an approval record, under every approver
// signer of p and maps the certificate to the identity it speaks for. Every
// reader of a person-signed approval (the recorder, the promoter and the
// client) goes through it, so a record becomes a decision in one place; each
// asks delivery.ReadsAsDecision of Class, so an agent's or a CI workload's
// record is a proposal. With no signer configured no record verifies.
func OpenPersonApproval(bundle []byte, p delivery.ClassPolicy) (PersonApproval, error) {
	if len(p.Signers) == 0 {
		return PersonApproval{}, errors.New("no approver signer is configured, so no signed approval counts")
	}
	var errs []error
	for _, signer := range p.Signers {
		var who delivery.PublisherIdentity
		var at time.Time
		st, id, err := delivery.OpenApproval(bundle, func(raw []byte) (err error) {
			who, at, err = VerifyPerson(raw, signer.Issuer, signer.TrustedRoot)
			return err
		})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if st.Predicate.Signer.Kind != delivery.SignerPerson {
			return PersonApproval{}, errors.New("the record does not say a person signed it")
		}
		// ponytail: the default rule classifies; the signed policy's signers
		// reach the promoter's registry watch only, not this path.
		class, by := delivery.ClassifySigner(who, nil)
		identity, source, err := delivery.ApproverIdentity(signer.Issuer, who.Subject, p)
		return PersonApproval{Statement: st, ID: id, Issuer: signer.Issuer, SAN: who.Subject, Identity: identity, Source: source,
			Class: class, ClassifiedBy: by, At: at}, err
	}
	return PersonApproval{}, errors.Join(errs...)
}
