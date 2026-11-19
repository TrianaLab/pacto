package publish

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// signedBy is the sign check a declaration's caller passes: the person it
// names signs, whatever the publish section says.
var signedBy = Check{Name: "sign", Passed: true, Message: "signed keyless by the person who logged in"}

// contractOnly is a contract-only input for payments at version.
func contractOnly(b *contract.Bundle) Input {
	return Input{Bundle: b, Revision: "declare:abc", ContractOnly: true, Checks: []Check{signedBy}}
}

func checkNames(p Plan) []string {
	var n []string
	for _, c := range p.Checks {
		n = append(n, c.Name)
	}
	return n
}

func TestContractOnlyPlansOnlyTheBundle(t *testing.T) {
	host, reg := newRegistry(t)
	p := MakePlan(context.Background(), reg, contractOnly(bundleOf(host, "1.0.0", workloadService)))
	expect(t, line(p.Ready, p.Missing, p.ContractOnly, states(p)) == "true 1 true bundle=absent", "plan: %v %s", p.Ready, states(p))
	expect(t, slices.Equal(checkNames(p), []string{"sign", "revision", "version"}), "checks: %v", checkNames(p))
}

func TestContractOnlyFirstVersionDeployNone(t *testing.T) {
	host, reg := newRegistry(t)
	p := MakePlan(context.Background(), reg, contractOnly(bundleOf(host, "0.1.0", workloadService)))
	expect(t, p.Deploy == "none", "deploy: %q", p.Deploy)
}

func TestContractOnlyApplyBuildsNothing(t *testing.T) {
	host, reg := newRegistry(t)
	tools := newTools()
	tools.failSBOM = true // reading an SBOM would fail the publish
	res, err := Apply(context.Background(), reg, tools, contractOnly(bundleOf(host, "1.0.0", workloadService)))
	expect(t, err == nil, "apply: %v", err)
	expect(t, len(tools.builds) == 0, "builds: %v", tools.builds)
	bundle := host + "/pacto/payments@" + res.Digests[BundleItem]
	expect(t, len(tools.signed) == 1 && tools.signed[bundle], "signed: %v", tools.signed)
	for _, repo := range []string{"/payments", "/charts/payments", "/deploy/payments"} {
		tags, _ := reg.Tags(context.Background(), host+repo)
		expect(t, len(tags) == 0, "%s tags: %v", repo, tags)
	}
	h, _ := reg.Head(context.Background(), host+"/pacto/payments:1.0.0")
	expect(t, h != nil && h.Digest == res.Digests[BundleItem], "bundle tag: %+v", h)
}

func TestContractOnlyKeepsBaselineDeploy(t *testing.T) {
	host, reg := newRegistry(t)
	in := contractOnly(bundleOf(host, "1.0.0", workloadService))
	in.BaselineDeploy = host + "/deploy/payments@sha256:" + strings.Repeat("a", 64)
	res, err := Apply(context.Background(), reg, newTools(), in)
	expect(t, err == nil && res.Plan.Deploy == in.BaselineDeploy && res.Deploy == "", "deploy: %v %q %q", err, res.Plan.Deploy, res.Deploy)
	h, _ := reg.Head(context.Background(), host+"/deploy/payments:1.0.0")
	expect(t, h == nil, "a deploy tag was written: %+v", h)
}

func TestContractOnlyVersionRuleStillApplies(t *testing.T) {
	host, reg := newRegistry(t)
	pushBundle(t, reg, host, "1.0.0", bundleOf(host, "1.0.0", workloadService))
	p := MakePlan(context.Background(), reg, contractOnly(bundleOf(host, "1.0.1", "job")))
	expect(t, !p.Ready && failedCheck(p) == "version" && p.Baseline == "1.0.0", "plan: %v %s %+v", p.Ready, failedCheck(p), p.Checks)
}

func TestContractOnlyExistingTagConflicts(t *testing.T) {
	host, reg := newRegistry(t)
	pushBundle(t, reg, host, "1.0.0", bundleOf(host, "1.0.0", "job"))
	_, err := Apply(context.Background(), reg, newTools(), contractOnly(bundleOf(host, "1.0.0", workloadService)))
	expect(t, errors.Is(err, ErrNotReady) && strings.Contains(err.Error(), "conflict"), "existing tag: %v", err)
}

func TestContractOnlyWithoutPublishSection(t *testing.T) {
	host, reg := newRegistry(t)
	in := contractOnly(sectionless(host, "0.1.0"))
	in.Publish = &contract.Publish{Bundle: "oci://" + host + "/payments"}
	p := MakePlan(context.Background(), reg, in)
	expect(t, p.Ready && states(p) == "bundle=absent" && p.Items[0].Ref == host+"/payments:0.1.0", "plan: %v %s", p.Ready, states(p))
}

// signRecorder records the sign configuration every Sign call receives.
type signRecorder struct {
	*fakeTools
	got []contract.PublishSign
}

func (s *signRecorder) Sign(ctx context.Context, sign contract.PublishSign, ref string) (bool, error) {
	s.got = append(s.got, sign)
	return s.fakeTools.Sign(ctx, sign, ref)
}

func TestContractOnlyNilSignUsesSignOf(t *testing.T) {
	host, reg := newRegistry(t)
	in := contractOnly(sectionless(host, "0.1.0"))
	in.Publish = &contract.Publish{Bundle: "oci://" + host + "/payments"}
	tools := &signRecorder{fakeTools: newTools()}
	_, err := Apply(context.Background(), reg, tools, in)
	expect(t, err == nil && len(tools.got) == 1 && tools.got[0] == (contract.PublishSign{}), "sign: %v %+v", err, tools.got)
}

func TestNextDraft(t *testing.T) {
	for _, c := range []struct {
		tags []string
		want int
	}{
		{nil, 1},
		{[]string{"1.0.0", "latest"}, 1},
		{[]string{"draft-1", "draft-2", "draft-9", "draft-x", "draft-"}, 10},
		{[]string{"pacto-accept-draft-4"}, 1},
	} {
		expect(t, NextDraft(c.tags, DraftPrefix) == c.want, "%v: %d", c.tags, NextDraft(c.tags, DraftPrefix))
	}
	expect(t, NextDraft([]string{"pacto-accept-draft-4", "draft-7"}, "pacto-accept-draft-") == 5, "other prefix")
}

func TestApplyDraftNextNumber(t *testing.T) {
	host, reg := newRegistry(t)
	other := bundleOf(host, "1.0.0", "job")
	for _, tag := range []string{"draft-1", "draft-2", "draft-9", "draft-x"} {
		pushBundle(t, reg, host, tag, other)
	}
	res, tag, err := ApplyDraft(context.Background(), reg, newTools(), Input{Bundle: bundleOf(host, "1.0.0", workloadService)}, true)
	expect(t, err == nil && tag == "draft-10" && res.Plan.Version == "draft-10" && res.Plan.ContractOnly, "draft: %v %s", err, tag)
	h, _ := reg.Head(context.Background(), host+"/pacto/payments:draft-10")
	expect(t, h != nil && h.Digest == res.Digests[BundleItem], "draft tag: %+v", h)
	expect(t, slices.Contains(res.Actions, "tagged the draft "+host+"/pacto/payments:draft-10"), "actions: %v", res.Actions)
}

// racingTags hands the next races tag writes to another run: that run's
// digest lands on the tag and this one's never does.
type racingTags struct {
	*Client
	other string
	races int
}

func (r *racingTags) Tag(ctx context.Context, repo, digest, tag string) error {
	if r.races > 0 {
		r.races--
		return r.Client.Tag(ctx, repo, r.other, tag)
	}
	return r.Client.Tag(ctx, repo, digest, tag)
}

func racing(t *testing.T, races int) (*Client, string, string, *Result, string, error) {
	t.Helper()
	host, client := newRegistry(t)
	other := pushBundle(t, client, host, "1.0.0", bundleOf(host, "1.0.0", "job"))
	tools := newTools()
	res, tag, err := ApplyDraft(context.Background(), &racingTags{Client: client, other: other, races: races}, tools, Input{Bundle: bundleOf(host, "1.0.0", workloadService)}, true)
	expect(t, len(tools.signed) == 1, "signed: %v", tools.signed)
	return client, host, other, res, tag, err
}

func TestApplyDraftRetriesOnceOnRace(t *testing.T) {
	client, host, other, _, tag, err := racing(t, 1)
	expect(t, err == nil && tag == "draft-2", "retry: %v %s", err, tag)
	h, _ := client.Head(context.Background(), host+"/pacto/payments:draft-1")
	expect(t, h.Digest == other, "draft-1 was overwritten: %+v", h)
}

func TestApplyDraftFailsAfterSecondRace(t *testing.T) {
	_, _, _, _, tag, err := racing(t, 2)
	expect(t, errors.Is(err, ErrTagRace) && tag == "" && strings.Contains(err.Error(), "two draft tags"), "second race: %v %s", err, tag)
}

// staleTags lists no tag, as a listing taken before another run wrote one.
type staleTags struct{ *Client }

func (staleTags) Tags(context.Context, string) ([]string, error) { return nil, nil }

func TestApplyDraftNeverOverwrites(t *testing.T) {
	host, client := newRegistry(t)
	other := pushBundle(t, client, host, "draft-1", bundleOf(host, "1.0.0", "job"))
	ctx := context.Background()
	local := Input{Bundle: bundleOf(host, "1.0.0", workloadService)}
	_, tag, err := ApplyDraft(ctx, staleTags{client}, newTools(), local, false)
	expect(t, err == nil && tag == "draft-2", "next free: %v %s", err, tag)
	pushBundle(t, client, host, "draft-3", bundleOf(host, "1.0.0", "job"))
	pushBundle(t, client, host, "draft-4", bundleOf(host, "1.0.0", "job"))
	// A listing that names only draft-2 leaves draft-3 and draft-4 taken.
	_, tag, err = ApplyDraft(ctx, listing{client, []string{"draft-2"}}, newTools(), local, false)
	expect(t, errors.Is(err, ErrTagRace) && tag == "", "both taken: %v %s", err, tag)
	for _, d := range []string{"draft-1", "draft-3", "draft-4"} {
		h, _ := client.Head(ctx, host+"/pacto/payments:"+d)
		expect(t, h != nil && h.Digest == other, "%s retagged: %+v", d, h)
	}
}

// listing lists tags in place of the registry's own.
type listing struct {
	*Client
	tags []string
}

func (l listing) Tags(context.Context, string) ([]string, error) { return l.tags, nil }

func TestApplyDraftUnsigned(t *testing.T) {
	host, reg := newRegistry(t)
	tools := newTools()
	_, tag, err := ApplyDraft(context.Background(), reg, tools, Input{Bundle: bundleOf(host, "1.0.0", workloadService)}, false)
	expect(t, err == nil && tag == "draft-1" && len(tools.signed) == 0, "unsigned: %v %s %v", err, tag, tools.signed)
}

func TestApplyDraftFailures(t *testing.T) {
	host, client := newRegistry(t)
	ctx := context.Background()
	local := Input{Bundle: bundleOf(host, "1.0.0", workloadService)}
	_, _, err := ApplyDraft(ctx, client, newTools(), Input{Bundle: sectionless(host, "1.0.0")}, false)
	expect(t, err != nil && strings.Contains(err.Error(), "bundle repository"), "no section: %v", err)
	unpackable := bundleOf(host, "1.0.0", workloadService)
	unpackable.FS = nil
	_, _, err = ApplyDraft(ctx, client, newTools(), Input{Bundle: unpackable}, false)
	expect(t, err != nil && strings.Contains(err.Error(), "packing"), "unpackable: %v", err)
	failSign := newTools()
	failSign.failSign = true
	for name, c := range map[string]struct {
		reg   Registry
		tools Tools
		want  string
	}{
		"tags":  {brokenRegistry{client, "tags"}, newTools(), "listing the drafts"},
		"head":  {brokenRegistry{client, "head"}, newTools(), "could not be read"},
		"write": {brokenRegistry{client, "write-bundle"}, newTools(), "pushing the bundle"},
		"sign":  {client, failSign, "signing"},
		"tag":   {failingTags{client, host + "/pacto/payments"}, newTools(), "tagging"},
	} {
		_, tag, err := ApplyDraft(ctx, c.reg, c.tools, local, true)
		expect(t, err != nil && tag == "" && strings.Contains(err.Error(), c.want) && !errors.Is(err, ErrTagRace), "%s: %v", name, err)
	}
}

func TestDraftTagNeverABase(t *testing.T) {
	tags := []string{"1.0.0", "draft-1", "draft-2"}
	for _, v := range []string{"", "1.1.0"} {
		expect(t, slices.Equal(Bases(tags, v), []string{"1.0.0"}), "%q: %v", v, Bases(tags, v))
	}
}

func TestDraftTagNeverDerivesAVersion(t *testing.T) {
	host, reg := newRegistry(t)
	d := pushBundle(t, reg, host, "1.0.0", deriveBundle(host, "1.0.0", workloadService, true))
	for _, tag := range []string{"draft-1", "draft-2"} {
		pushBundle(t, reg, host, tag, deriveBundle(host, "3.0.0", "job", false))
	}
	got, err := DeriveVersion(context.Background(), reg, DeriveInput{Repo: host + "/pacto/payments", Local: same(workloadService, true)(host),
		Unchanged: never, Verify: signedSet{host + "/pacto/payments@" + d: true}.verify})
	expect(t, err == nil && got.Baseline == "1.0.0" && len(got.Notes) == 0, "derived: %v %+v", err, got)
}

func TestVersionCheckIgnoresDraftTags(t *testing.T) {
	host, reg := newRegistry(t)
	pushBundle(t, reg, host, "draft-1", bundleOf(host, "9.0.0", "job"))
	p := MakePlan(context.Background(), reg, contractOnly(bundleOf(host, "1.0.0", workloadService)))
	ch := p.Checks[len(p.Checks)-1]
	expect(t, ch.Name == "version" && ch.Passed && p.Baseline == "" && strings.Contains(ch.Message, "no earlier version"), "version: %+v", ch)
}
