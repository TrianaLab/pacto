/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package member makes the operator of a joined cluster a member: it dials the
// hub (memberlink.Member) and answers the hub's per-person reviews with this
// cluster's own authentication and RBAC. It opens no port and impersonates
// nobody.
package member

import (
	"context"
	"regexp"
	"strings"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/trianalab/pacto/v4/pkg/memberlink"
)

// maxMappedGroups bounds the groups of a mapped identity.
const maxMappedGroups = 64

// mappedUser is the shape of a mapped user: the hub binds the issuer (the
// first 12 hex of its sha256) into the name, so two issuers never share one.
var mappedUser = regexp.MustCompile(`^pacto-mapped:[0-9a-f]{12}:.+$`)

// Reviewer answers the hub's reviews in this cluster. It trusts nothing the
// hub asserts about a token: this cluster's apiserver says who it is.
type Reviewer struct {
	// Own is the operator's rest.Config; the token review keeps only its
	// server and CA.
	Own *rest.Config
	// Client runs the SubjectAccessReview as the operator.
	Client client.Client
	// NewClient builds the client the token review runs with.
	NewClient func(*rest.Config) (client.Client, error)
	// Now is the clock tokens are judged against.
	Now func() time.Time
}

// Answer reviews r: whether the person r names may list r's resource in r's
// namespace here. It always asks list, refuses resources outside
// memberlink.ReviewableResources and never logs or keeps the token.
func (rv *Reviewer) Answer(ctx context.Context, r memberlink.Review) memberlink.Answer {
	if !memberlink.Reviewable(r.Group, r.Resource) {
		return refused(memberlink.RefusedReviewFailed)
	}
	ctx, cancel := context.WithTimeout(ctx, memberlink.ReviewTimeout)
	defer cancel()
	switch {
	case r.Token != "" && r.Mapped == nil:
		return rv.answerToken(ctx, r)
	case r.Token == "" && r.Mapped != nil:
		return rv.answerMapped(ctx, r)
	}
	return refused(memberlink.RefusedNoIdentity)
}

func refused(why string) memberlink.Answer { return memberlink.Answer{Refused: why} }

// answerToken asks this cluster who the token is, then whether that person
// may list the resource: the shared-issuer model.
func (rv *Reviewer) answerToken(ctx context.Context, r memberlink.Review) memberlink.Answer {
	if memberlink.TokenFresh(r.Token, rv.Now()) != nil {
		return refused(memberlink.RefusedTokenExpired)
	}
	ui, err := rv.selfReview(ctx, r.Token)
	if apierrors.IsUnauthorized(err) {
		return refused(memberlink.RefusedIssuerNotAccepted)
	}
	if err != nil {
		return refused(memberlink.RefusedReviewFailed)
	}
	if clusterLocal(ui.Username, ui.Groups) {
		return refused(memberlink.RefusedClusterLocal)
	}
	extra := map[string]authorizationv1.ExtraValue{}
	for k, v := range ui.Extra {
		extra[k] = authorizationv1.ExtraValue(v)
	}
	spec := authorizationv1.SubjectAccessReviewSpec{User: ui.Username, Groups: ui.Groups, UID: ui.UID, Extra: extra}
	return rv.accessReview(ctx, r, spec, memberlink.ModelSharedIssuer)
}

// answerMapped reviews an identity the hub mapped through a person-approved
// group mapping record: the group-mapping model.
func (rv *Reviewer) answerMapped(ctx context.Context, r memberlink.Review) memberlink.Answer {
	m := r.Mapped
	if !mappedValid(m) {
		return refused(memberlink.RefusedMappingInvalid)
	}
	spec := authorizationv1.SubjectAccessReviewSpec{User: m.User, Groups: m.Groups}
	return rv.accessReview(ctx, r, spec, memberlink.ModelGroupMapping)
}

// mappedValid holds a mapped identity to the hub's shape: an issuer-bound
// user, a record digest, at most maxMappedGroups and no system: group.
func mappedValid(m *memberlink.Mapped) bool {
	if !mappedUser.MatchString(m.User) || !strings.HasPrefix(m.Record, "sha256:") || len(m.Groups) > maxMappedGroups {
		return false
	}
	for _, g := range m.Groups {
		if strings.HasPrefix(g, "system:") {
			return false
		}
	}
	return true
}

// clusterLocal says whether an identity belongs to this cluster alone: a
// ServiceAccount or a node is reviewed only at home.
func clusterLocal(user string, groups []string) bool {
	if strings.HasPrefix(user, "system:serviceaccount:") || strings.HasPrefix(user, "system:node:") {
		return true
	}
	for _, g := range groups {
		if g == "system:serviceaccounts" || g == "system:nodes" || strings.HasPrefix(g, "system:serviceaccounts:") {
			return true
		}
	}
	return false
}

// selfReview asks this cluster's apiserver who token is, with the token as
// the only credential: it travels in the Authorization header, never in a
// request body.
func (rv *Reviewer) selfReview(ctx context.Context, token string) (authenticationv1.UserInfo, error) {
	cfg := rest.AnonymousClientConfig(rv.Own)
	cfg.BearerToken = token
	c, err := rv.NewClient(cfg)
	if err != nil {
		return authenticationv1.UserInfo{}, err
	}
	ssr := &authenticationv1.SelfSubjectReview{}
	if err := c.Create(ctx, ssr); err != nil {
		return authenticationv1.UserInfo{}, err
	}
	return ssr.Status.UserInfo, nil
}

// accessReview asks, as the operator, whether spec's person may list r's
// resource in r's namespace. A refusal that carries an evaluation error is no
// answer.
func (rv *Reviewer) accessReview(ctx context.Context, r memberlink.Review, spec authorizationv1.SubjectAccessReviewSpec,
	model string) memberlink.Answer {
	spec.ResourceAttributes = &authorizationv1.ResourceAttributes{
		Namespace: r.Namespace, Verb: "list", Group: r.Group, Resource: r.Resource}
	sar := &authorizationv1.SubjectAccessReview{Spec: spec}
	if err := rv.Client.Create(ctx, sar); err != nil {
		return refused(memberlink.RefusedReviewFailed)
	}
	if !sar.Status.Allowed && sar.Status.EvaluationError != "" {
		return refused(memberlink.RefusedReviewFailed)
	}
	return memberlink.Answer{Allowed: sar.Status.Allowed, Model: model}
}
