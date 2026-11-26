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

// VerifyPersonRecord opens a person's record (a what) through open, under
// the approver signer of policy whose issuer its certificate names, and maps
// the certificate to the user the record must name, the one open returns. It
// returns that user, the verified signing time, the signer's class
// (delivery.ClassifySigner; empty when the signature did not verify) and why
// the record does not verify, "" when it does. Every reader of a record a
// person signs keyless (join records, group mappings, member requests) goes
// through it.
func VerifyPersonRecord(policy delivery.ClassPolicy, what string, open func(verify func([]byte) error) (named string, err error)) (string, time.Time, delivery.ActorClass, string) {
	var signer delivery.PublisherIdentity
	var at time.Time
	named, err := open(func(b []byte) error {
		var errs []error
		for _, s := range policy.Signers {
			id, t, err := VerifyPerson(b, s.Issuer, s.TrustedRoot)
			if err == nil {
				signer, at = id, t
				return nil
			}
			errs = append(errs, err)
		}
		return errors.Join(errs...)
	})
	if err != nil {
		return "", at, "", "a " + what + " was not counted: " + err.Error()
	}
	class, _ := delivery.ClassifySigner(signer, nil)
	id, source, err := delivery.ApproverIdentity(signer.Issuer, signer.Subject, policy)
	if err == nil {
		if code, why := delivery.KeylessRefusal(id, source); code != "" {
			err = errors.New(why)
		}
	}
	switch {
	case err != nil:
		return id.User, at, class, "a " + what + " signed by " + signer.Subject + " was not counted: " + err.Error()
	case id.User != named:
		return id.User, at, class, "a " + what + " claiming approver " + named + " is signed as " + id.User + " and was not counted"
	}
	return id.User, at, class, ""
}

// VerifyJoinRecord opens bundle, a join record, as VerifyPersonRecord does
// under the approver signers of policy: Verified says it verified and maps
// to the approver it names, Why says why not. Counted is left to the reader.
func VerifyJoinRecord(policy delivery.ClassPolicy, bundle []byte) delivery.VerifiedJoin {
	var v delivery.VerifiedJoin
	v.User, v.SignedAt, _, v.Why = VerifyPersonRecord(policy, "join record", func(verify func([]byte) error) (string, error) {
		st, err := delivery.OpenClusterJoin(bundle, verify)
		v.Record = st.Predicate
		return st.Predicate.Approver, err
	})
	v.Verified = v.Why == ""
	return v
}
