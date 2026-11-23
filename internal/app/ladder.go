package app

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// ErrLadderChanged is why a confirmation of a proposal other than the one the
// promoter publishes now gets nothing signed.
var ErrLadderChanged = errors.New("the proposal changed: run pacto delivery inbox again")

// LadderView reads the installation ladder the promoter publishes
// (delivery.EnvironmentsConfigMap) in the delivery namespace, with the
// caller's own credentials.
func (d *Delivery) LadderView(ctx context.Context) (delivery.EnvironmentsView, error) {
	ns := cmp.Or(d.PolicyNamespace, delivery.PolicyNamespace)
	var cm struct {
		Data map[string]string `json:"data"`
	}
	err := getInto(ctx, d.Dynamic.Resource(gvrConfigMaps).Namespace(ns), delivery.EnvironmentsConfigMap, &cm)
	switch {
	case apierrors.IsNotFound(err):
		return delivery.EnvironmentsView{}, fmt.Errorf("the promoter publishes no installation ladder in %s: it needs delivery.environments enabled on the pacto-operator chart", ns)
	case err != nil:
		return delivery.EnvironmentsView{}, fmt.Errorf("reading the installation ladder in %s: %w", ns, err)
	}
	var v delivery.EnvironmentsView
	if err := json.Unmarshal([]byte(cm.Data["view.json"]), &v); err != nil {
		return delivery.EnvironmentsView{}, fmt.Errorf("the installation ladder in %s/%s: %w", ns, delivery.EnvironmentsConfigMap, err)
	}
	return v, nil
}

// LadderRungs is the installation ladder a person confirms from view: its
// declared chain, then the rungs confirmed before and every proposed rung
// but those in drop, each a leaf, and the dropped rungs, sorted. The newest
// record is the whole confirmed ladder, so the rungs confirmed before are
// signed again. A dropped rung must be proposed: a declared one comes from
// the chart value.
func LadderRungs(view delivery.EnvironmentsView, drop []string) ([]delivery.LadderRung, []string, error) {
	dropped := slices.Compact(slices.Sorted(slices.Values(drop)))
	for _, n := range dropped {
		named := func(r delivery.LadderRung) bool { return r.Name == n }
		switch {
		case slices.ContainsFunc(view.Declared, named):
			return nil, nil, fmt.Errorf("%s comes from delivery.environments.ladder: change the chart value", n)
		case !slices.ContainsFunc(view.Proposal, named):
			var names []string
			for _, r := range view.Proposal {
				names = append(names, r.Name)
			}
			return nil, nil, fmt.Errorf("%s is not proposed: the proposal holds %s", n, strings.Join(names, ", "))
		}
	}
	rungs := slices.Clone(view.Declared)
	for _, r := range slices.Concat([]delivery.LadderRung(view.Confirmed), view.Proposal) {
		if !slices.Contains(dropped, r.Name) {
			rungs = append(rungs, delivery.LadderRung{Name: r.Name, Source: r.Source, Provenance: delivery.RungInferred})
		}
	}
	return rungs, dropped, nil
}

// ErrNamespacesChanged is why a confirmation of namespace candidates other
// than the ones the promoter lists now gets nothing signed.
var ErrNamespacesChanged = errors.New("the namespaces waiting to be created changed: run pacto delivery ladder confirm --create-namespaces again")

// NamespaceCandidates reads, with the caller's own credentials, the
// namespaces the promoter would create once a person allows it
// (delivery.NamespaceCandidatesKey of delivery.PlacementsConfigMap). Only
// whoever may read that ConfigMap may sign them.
func (d *Delivery) NamespaceCandidates(ctx context.Context) ([]string, error) {
	ns := cmp.Or(d.PolicyNamespace, delivery.PolicyNamespace)
	var cm struct {
		Data map[string]string `json:"data"`
	}
	err := getInto(ctx, d.Dynamic.Resource(gvrConfigMaps).Namespace(ns), delivery.PlacementsConfigMap, &cm)
	switch {
	case apierrors.IsNotFound(err):
		return nil, nil
	case apierrors.IsForbidden(err):
		return nil, fmt.Errorf("you may not read the namespaces waiting to be created in %s/%s, so you cannot allow them: %w", ns, delivery.PlacementsConfigMap, err)
	case err != nil:
		return nil, fmt.Errorf("reading the namespaces waiting to be created in %s: %w", ns, err)
	}
	var out []string
	if v := cm.Data[delivery.NamespaceCandidatesKey]; v != "" {
		if err := json.Unmarshal([]byte(v), &out); err != nil {
			return nil, fmt.Errorf("the namespaces waiting to be created in %s/%s: %w", ns, delivery.PlacementsConfigMap, err)
		}
	}
	return out, nil
}

// WriteLadder signs rec, a person's confirmation of the installation ladder,
// with signer and pushes it on the cluster's ladder stream in repo, the
// approvals repository the promoter reads ladder records from, anchoring the
// stream first. It reads the installation ladder again first and refuses when
// its proposal, or the namespaces rec lets Pacto create, are no longer the
// ones rec confirms, so a proposal that changed while the person logged in
// gets nothing signed. It returns where the record
// is and the form it took.
func (d *Delivery) WriteLadder(ctx context.Context, rec delivery.LadderRecord, signer delivery.Signer, repo string) (string, capabilities.ReferrersMode, error) {
	view, err := d.LadderView(ctx)
	if err != nil {
		return "", "", err
	}
	if view.ProposalDigest != rec.ProposalDigest {
		return "", "", ErrLadderChanged
	}
	if rec.NamespacesDigest != "" {
		ns, err := d.NamespaceCandidates(ctx)
		if err != nil {
			return "", "", err
		}
		if delivery.NamespacesDigest(ns) != rec.NamespacesDigest {
			return "", "", ErrNamespacesChanged
		}
	}
	if err := rec.Check(d.Now()); err != nil {
		return "", "", err
	}
	r, err := d.Records(repo)
	if err != nil {
		return "", "", err
	}
	ctx, cancel := delivery.WithRegistryTimeout(ctx)
	defer cancel()
	signed, err := delivery.SignLadder(ctx, signer, delivery.NewLadderStatement(rec))
	if err != nil {
		return "", "", err
	}
	stream := delivery.LadderStream(rec.ClusterID)
	caps, err := deliveryrecord.EnsureAnchor(ctx, r, stream)
	if err != nil {
		return "", "", err
	}
	ref, err := deliveryrecord.Push(ctx, r, stream, caps.Referrers, delivery.PredicateEnvironmentLadder, signed, nil)
	return ref, caps.Referrers, err
}
