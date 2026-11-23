package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/kubecluster"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

const (
	// clusterGrant is the grant an approver needs, to their own user.
	clusterGrant = "kubectl create clusterrole pacto-cluster-approver --verb=approve --resource=clusterjoins.pacto.trianalab.io, bound to your user"
	// needsRegistry is why a cluster command has no repository to read.
	needsRegistry = "name the registry with --registry, or the operator's detection with --detected k8s:<namespace>/<release>-detected once registry is set on the hub's operator"
	// minClusterPrefix is the shortest cluster id prefix a decision takes.
	minClusterPrefix = 8
)

func newClusterCommand(svc *app.Service, v *viper.Viper, d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "List, approve and remove the member clusters of this hub, and map their groups",
		Long: "Every cluster whose operator sets registry publishes a signed snapshot to <registry>/pacto-targets:cluster-<uid>. " +
			"This cluster, the hub, reads them: a cluster stays pending until a person approves its key, and only an approved member's " +
			"services, targets and workloads join the hub's fleet. The hub is the current kubeconfig context or --context.",
	}
	cmd.AddCommand(newClusterListCommand(svc, v, d), newClusterDecisionCommand(svc, v, d, delivery.JoinApprove), newClusterDecisionCommand(svc, v, d, delivery.JoinRemove),
		newClusterMapGroupsCommand(svc, v, d))
	return cmd
}

// addClusterHubFlags adds the flags that name the hub and its registry.
func addClusterHubFlags(cmd *cobra.Command) {
	// A string array, as the fleet's shared --context is, though a hub is one.
	cmd.Flags().StringArray("context", nil, "kubeconfig context of the hub cluster (default the current context)")
	cmd.Flags().String("registry", "", "the registry the members publish to, or its pacto-targets repository; this or --detected is required")
	cmd.Flags().String("detected", "", "what the hub's operator detected, read for the registry it advertises: k8s:<namespace>/<name> of its <release>-detected ConfigMap, or a file holding the same JSON")
	cmd.Flags().String("delivery-namespace", delivery.PolicyNamespace, "namespace of the hub's delivery identities ConfigMap, which lists the approver signers")
}

func newClusterListCommand(svc *app.Service, v *viper.Viper, d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the clusters that publish to this hub's registry and where each stands",
		Long: "Lists every cluster in <registry>/pacto-targets: approved members, pending clusters with why they are pending, and removed members. " +
			"The environments a cluster seems to host are inferred and say where they came from. An approved member's group mappings say who approved them. " +
			"The list reads the registry, so it knows nothing about live review channels: the hub's dashboard shows them on Data sources and in meta.clusters[].link of its fleet answers.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, opts, err := clusterHub(cmd, d)
			if err != nil {
				return err
			}
			// The person's own list: every row, and approve offered; the
			// approve command checks the right itself.
			l := visibleJoins(svc.ClusterJoins(cmd.Context(), opts), true, nil)
			return formatResult(cmd, v.GetString(outputFormatKey), l,
				func() error { printClusterList(cmd.OutOrStdout(), l); return nil },
				func() error { printClusterListMarkdown(cmd.OutOrStdout(), l); return nil })
		},
	}
	addClusterHubFlags(cmd)
	return cmd
}

func newClusterDecisionCommand(svc *app.Service, v *viper.Viper, d deps, dec delivery.JoinDecision) *cobra.Command {
	cmd := &cobra.Command{
		Use:   string(dec) + " <uid>",
		Short: "Remove a member cluster from this hub, signed as you",
		Long: "Signs a revocation as you, keyless after a fresh login in your browser, and records it beside the member's snapshot. " +
			"The member's services leave the hub's fleet from its next build. You need approve on clusterjoins.pacto.trianalab.io in the hub, " +
			"granted to your user (" + clusterGrant + "), and the hub's operator must list approver signers in delivery.identities.signers.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return decideCluster(cmd, svc, v, d, dec, args[0])
		},
	}
	if dec == delivery.JoinApprove {
		cmd.Short = "Approve a cluster as a member of this hub, signed as you"
		cmd.Long = "Signs an approval of the cluster's key as you, keyless after a fresh login in your browser, and records it beside the cluster's snapshot. " +
			"From the hub's next fleet build on, the member's services, targets and workloads join it, verified under the approved key. " +
			"--key names the key you checked: compare it out of band with the snapshotKeyID in the member's own <release>-detected ConfigMap. " +
			"You need approve on clusterjoins.pacto.trianalab.io in the hub, granted to your user (" + clusterGrant + "); " +
			"groups are not checked, because a certificate names none. The hub's operator must list approver signers in delivery.identities.signers."
		cmd.Example = "  pacto cluster approve 9f1c4e2a --key sha256:91ab...e7 --name eu-prod --registry ghcr.io/acme"
		cmd.Flags().String("key", "", "the member key you checked, sha256:<64 hex> (required)")
		cmd.Flags().String("name", "", "display name for the member, a DNS label (default the name its snapshot gives)")
		_ = cmd.MarkFlagRequired("key")
	}
	addClusterHubFlags(cmd)
	cmd.Flags().String("issuer", "", "the approver signer's OIDC issuer, when the hub lists several")
	return cmd
}

// clusterHub reads the hub cmd names: a client with the person's own
// credentials and how to read its joins.
func clusterHub(cmd *cobra.Command, d deps) (dynamic.Interface, app.JoinOptions, error) {
	contexts, _ := cmd.Flags().GetStringArray("context")
	registry, _ := cmd.Flags().GetString("registry")
	detected, _ := cmd.Flags().GetString("detected")
	ns, _ := cmd.Flags().GetString("delivery-namespace")
	switch {
	case len(contexts) > 1:
		return nil, app.JoinOptions{}, errors.New("name one hub cluster with --context")
	case registry == "" && detected == "":
		return nil, app.JoinOptions{}, errors.New(needsRegistry)
	}
	dyn, opts, err := hubJoins(cmd.Context(), d, strings.Join(contexts, ""), registry, detected, ns)
	if err == nil && opts.Repository == "" {
		err = errors.New(needsRegistry)
	}
	return dyn, opts, err
}

// hubJoins are how the hub at kubeconfig context (or the current one)
// reads its joins, asking with the caller's own credentials: the
// repository is registry's pacto-targets, else the one the detection at
// detected advertises ("" when it names none); the approver signers are
// read in policyNS (the chart's default when "").
func hubJoins(ctx context.Context, d deps, contextName, registry, detected, policyNS string) (dynamic.Interface, app.JoinOptions, error) {
	dyn, err := hubDynamic(d, contextName)
	if err != nil {
		return nil, app.JoinOptions{}, fmt.Errorf("reaching the hub cluster: %w", err)
	}
	opts, err := joinsOn(ctx, dyn, registry, detected, cmp.Or(policyNS, delivery.PolicyNamespace))
	return dyn, opts, err
}

// joinsOn are how the hub dyn reaches reads its joins, asking with dyn's
// credentials and reading its approver signers in policyNS.
func joinsOn(ctx context.Context, dyn dynamic.Interface, registry, detected, policyNS string) (app.JoinOptions, error) {
	hub, err := kubecluster.ID(ctx, dyn)
	if err != nil {
		return app.JoinOptions{}, err
	}
	repo := targetsRepository(registry)
	if repo == "" && detected != "" {
		if repo, err = app.AdvertisedTargets(ctx, dyn, detected, time.Now); err != nil {
			return app.JoinOptions{}, err
		}
	}
	return app.JoinOptions{Repository: repo, Hub: hub, Authorize: authn.SubjectAccessReview(dyn),
		Delivery: &app.Delivery{Dynamic: dyn, PolicyNamespace: policyNS, Now: time.Now}}, nil
}

// hubDynamic is a client of the kubeconfig context name, or of the
// current one.
func hubDynamic(d deps, name string) (dynamic.Interface, error) {
	if name == "" {
		return d.newDynamic()
	}
	cfg, err := d.contextConfig(name)
	if err != nil {
		return nil, err
	}
	return d.dynamicFor(cfg)
}

// targetsRepository is the pacto-targets repository of registry, which
// may name it already; "" for none.
func targetsRepository(registry string) string {
	registry = strings.TrimSuffix(strings.TrimPrefix(registry, "oci://"), "/")
	if registry == "" || strings.HasSuffix(registry, "/"+delivery.TargetSnapshotRepository) {
		return registry
	}
	return registry + "/" + delivery.TargetSnapshotRepository
}

// shortUID is a cluster id as rows show it.
func shortUID(uid string) string { return uid[:min(minClusterPrefix, len(uid))] }

// snapshotWords say how old a pending cluster's snapshot is when it is not
// fresh, so a person never approves a cluster from stale evidence.
func snapshotWords(c delivery.ClusterJoinState) string {
	if c.State != delivery.JoinPending || c.Status == "" || c.Status == fleet.SourceAvailable {
		return ""
	}
	if c.SnapshotAsOf == "" {
		return "snapshot " + string(c.Status)
	}
	return "snapshot " + string(c.Status) + " from " + c.SnapshotAsOf
}

// clusterLine is one member as a text row.
func clusterLine(c delivery.ClusterJoinState) string {
	parts := []string{fmt.Sprintf("%s (%s)", clusterName(c), c.NameSource), c.UID, string(c.State)}
	if w := snapshotWords(c); w != "" {
		parts = append(parts, w)
	}
	if h := hosts(c.Environments); h != "" {
		parts = append(parts, "hosts: "+h)
	}
	if c.KeyID != "" {
		parts = append(parts, "key sha256:"+c.KeyID)
	}
	switch {
	case c.State != delivery.JoinPending:
		parts = append(parts, "by "+c.Approver+" on "+c.ApprovedAt)
	case c.Reason != "":
		parts = append(parts, c.Reason)
	}
	if c.MayReplace != nil {
		parts = append(parts, "may replace "+c.MayReplace.Name)
	}
	if m := mappingWords(c); m != "" {
		parts = append(parts, "mapping "+m)
	}
	return strings.Join(parts, "  ")
}

// clusterName is c's display name, its short id when it has none.
func clusterName(c delivery.ClusterJoinState) string {
	if c.Name == "" {
		return shortUID(c.UID)
	}
	return c.Name
}

// hosts names the environments a member seems to host with their sources.
func hosts(envs []delivery.AdvertisedEnvironment) string {
	var out []string
	for _, e := range envs {
		out = append(out, e.Name+" (from "+e.Source+")")
	}
	return strings.Join(out, ", ")
}

// printClusterList prints l as text: the hub, one row per member, then the
// counts and limitations.
func printClusterList(w io.Writer, l delivery.ClusterJoinList) {
	_, _ = fmt.Fprintf(w, "hub %s reads %s (%s, as of %s)\n", l.Hub.UID, l.Repository, l.Completeness, l.AsOf)
	if len(l.Clusters) == 0 {
		_, _ = fmt.Fprintln(w, "no member clusters")
	}
	for _, c := range l.Clusters {
		_, _ = fmt.Fprintln(w, clusterLine(c))
	}
	printClusterCounts(w, l)
}

// printClusterCounts prints what l hid or did not count, and its limitations.
func printClusterCounts(w io.Writer, l delivery.ClusterJoinList) {
	if l.Hidden > 0 {
		_, _ = fmt.Fprintf(w, "%d clusters you may not approve are hidden\n", l.Hidden)
	}
	if l.OtherHubs > 0 {
		_, _ = fmt.Fprintf(w, "%d join or mapping records name another hub and were not used\n", l.OtherHubs)
	}
	for _, lim := range l.Limitations {
		_, _ = fmt.Fprintf(w, "[%s] %s\n", lim.Code, lim.Message)
	}
}

// printClusterListMarkdown prints l as a markdown table.
func printClusterListMarkdown(w io.Writer, l delivery.ClusterJoinList) {
	_, _ = fmt.Fprintf(w, "Hub `%s` reads `%s` (%s, as of %s)\n\n", l.Hub.UID, l.Repository, l.Completeness, l.AsOf)
	_, _ = fmt.Fprintln(w, "| Name | UID | State | Hosts | Key | Approver or reason | Mapping |")
	_, _ = fmt.Fprintln(w, "|---|---|---|---|---|---|---|")
	for _, c := range l.Clusters {
		state := string(c.State)
		if sw := snapshotWords(c); sw != "" {
			state += " (" + sw + ")"
		}
		who := c.Reason
		if c.State != delivery.JoinPending {
			who = c.Approver + " on " + c.ApprovedAt
		}
		if c.MayReplace != nil {
			who += "; may replace " + c.MayReplace.Name
		}
		key := ""
		if c.KeyID != "" {
			key = "sha256:" + c.KeyID
		}
		_, _ = fmt.Fprintf(w, "| %s (%s) | %s | %s | %s | %s | %s | %s |\n", clusterName(c), c.NameSource, c.UID, state, hosts(c.Environments), key,
			strings.ReplaceAll(who, "|", `\|`), cmp.Or(mappingWords(c), "-"))
	}
	_, _ = fmt.Fprintln(w)
	printClusterCounts(w, l)
}

// keyPattern is a key id as --key takes it.
var keyPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// clusterDecision is a decision on its way to a signed record.
type clusterDecision struct {
	cmd    *cobra.Command
	svc    *app.Service
	v      *viper.Viper
	d      deps
	dec    delivery.JoinDecision
	dyn    dynamic.Interface
	opts   app.JoinOptions
	member delivery.ClusterJoinState
	policy delivery.ClassPolicy
	signer delivery.PersonSigner
	user   string
}

// decideCluster signs dec on the cluster uid names as the person and records
// it (approveSteps): every check that can refuse runs before the login.
func decideCluster(cmd *cobra.Command, svc *app.Service, v *viper.Viper, d deps, dec delivery.JoinDecision, uid string) error {
	c := &clusterDecision{cmd: cmd, svc: svc, v: v, d: d, dec: dec}
	rec, done, err := c.approveSteps(uid)
	if err != nil || done {
		return err
	}
	return c.sign(rec)
}

// approveSteps resolves the member, refuses what would not count and
// returns the record to sign, or done when the member already stands so.
func (c *clusterDecision) approveSteps(uid string) (delivery.ClusterJoin, bool, error) {
	key, _ := c.cmd.Flags().GetString("key")
	name, _ := c.cmd.Flags().GetString("name")
	issuer, _ := c.cmd.Flags().GetString("issuer")
	if c.dec == delivery.JoinApprove && !keyPattern.MatchString(key) {
		return delivery.ClusterJoin{}, false, errors.New("--key wants sha256:<64 hex>: the member's snapshotKeyID")
	}
	var err error
	if c.dyn, c.opts, err = clusterHub(c.cmd, c.d); err != nil {
		return delivery.ClusterJoin{}, false, err
	}
	if fullUID.MatchString(uid) {
		c.opts.Include = uid // read past the bounds, and without a tag
	}
	if c.member, err = findCluster(c.svc.ClusterJoins(c.cmd.Context(), c.opts), uid); err != nil {
		return delivery.ClusterJoin{}, false, err
	}
	if done, err := c.standing(strings.TrimPrefix(key, "sha256:"), name); done || err != nil {
		return delivery.ClusterJoin{}, done, err
	}
	if err := c.pickSigner(issuer); err != nil {
		return delivery.ClusterJoin{}, false, err
	}
	if err := c.authority(); err != nil {
		return delivery.ClusterJoin{}, false, err
	}
	m := c.member
	keyID, pub := m.KeyID, m.PublicKey
	if c.dec == delivery.JoinRemove && m.ApprovedKeyID != "" {
		keyID, pub = m.ApprovedKeyID, m.ApprovedPublicKey // the key a removal revokes
	}
	rec := delivery.ClusterJoin{Cluster: m.UID, Hub: c.opts.Hub, KeyID: keyID, PublicKey: pub, Decision: c.dec,
		Name: name, Approver: c.user, At: time.Now().UTC().Format(time.RFC3339)}
	return rec, false, rec.Check()
}

// fullUID is a cluster UID in full, as Kubernetes writes one.
var fullUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// findCluster is the member of l uid names: its full id, or a prefix of at
// least 8 characters that names one member.
func findCluster(l delivery.ClusterJoinList, uid string) (delivery.ClusterJoinState, error) {
	var found []delivery.ClusterJoinState
	for _, c := range l.Clusters {
		if c.UID == uid {
			return c, nil
		}
		if len(uid) >= minClusterPrefix && strings.HasPrefix(c.UID, uid) {
			found = append(found, c)
		}
	}
	switch {
	case len(found) == 1:
		return found[0], nil
	case len(found) > 1:
		var ids []string
		for _, c := range found {
			ids = append(ids, c.UID)
		}
		return delivery.ClusterJoinState{}, fmt.Errorf("%s names %d clusters (%s): name more of the id", uid, len(found), strings.Join(ids, ", "))
	case len(uid) < minClusterPrefix:
		return delivery.ClusterJoinState{}, fmt.Errorf("name the cluster by its id or at least %d characters of it", minClusterPrefix)
	}
	if l.Completeness != fleet.CompletenessComplete {
		return delivery.ClusterJoinState{}, fmt.Errorf("no cluster %s was read from %s, and the list is partial: name its full id, which is read past the bounds", uid, l.Repository)
	}
	return delivery.ClusterJoinState{}, fmt.Errorf("no cluster %s publishes to %s", uid, l.Repository)
}

// standing refuses an approval of a member whose snapshot names no key it
// is signed by or another key than key, and a removal of a member with no
// key at all; it says done when the member already stands as asked.
func (c *clusterDecision) standing(key, name string) (bool, error) {
	m := c.member
	switch {
	case c.dec == delivery.JoinRemove && m.KeyID == "" && m.ApprovedKeyID == "",
		c.dec == delivery.JoinApprove && m.KeyID == "":
		return false, fmt.Errorf("the snapshot of %s names no key to decide on: %s", m.UID, m.Reason)
	case c.dec == delivery.JoinApprove && m.KeyID != key:
		return false, fmt.Errorf("ClusterKeyMismatch: the snapshot of %s is signed by sha256:%s; you checked sha256:%s", m.UID, m.KeyID, key)
	case c.dec == delivery.JoinApprove && m.State == delivery.JoinApproved && (name == "" || name == m.Name),
		c.dec == delivery.JoinRemove && m.State == delivery.JoinRemoved:
		_, _ = fmt.Fprintf(c.cmd.OutOrStdout(), "%s (%s) is already %s; nothing was signed\n", clusterName(m), shortUID(m.UID), m.State)
		return true, nil
	}
	return false, nil
}

// pickSigner picks the hub's approver signer of issuer.
func (c *clusterDecision) pickSigner(issuer string) error {
	p, err := c.opts.Delivery.ClassPolicy(c.cmd.Context())
	if err != nil {
		return fmt.Errorf("the hub's approver signers could not be read: %w", err)
	}
	c.policy = p
	if len(p.Signers) == 0 {
		return errors.New("this hub accepts no signed approvals: set delivery.identities.signers on the hub's operator")
	}
	s, ok := pickSigner(p, issuer)
	if !ok {
		var issuers []string
		for _, s := range p.Signers {
			issuers = append(issuers, s.Issuer)
		}
		return fmt.Errorf("pick the approver signer with --issuer: the hub accepts %s", strings.Join(issuers, ", "))
	}
	c.signer = s
	return nil
}

var gvrSelfAccess = schema.GroupVersionResource{Group: "authorization.k8s.io", Version: "v1", Resource: "selfsubjectaccessreviews"}

// authority checks, before any login, that the person may approve the
// member in the hub, and that the hub would count a record signed as their
// own user, which carries no groups.
func (c *clusterDecision) authority() error {
	ctx, uid := c.cmd.Context(), c.member.UID
	res := authn.ClusterJoinResource
	res.Name = uid
	review := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "authorization.k8s.io/v1", "kind": "SelfSubjectAccessReview",
		"spec": map[string]any{"resourceAttributes": map[string]any{"verb": res.Verb, "group": res.Group, "resource": res.Resource, "name": uid}}}}
	out, err := c.dyn.Resource(gvrSelfAccess).Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("asking the hub whether you may approve clusterjoins: %w", err)
	}
	if ok, _, _ := unstructured.NestedBool(out.Object, "status", "allowed"); !ok {
		return fmt.Errorf("ClusterApproveForbidden: you may not approve clusterjoins named %s in the hub; ask for %s", uid, clusterGrant)
	}
	me, _, err := authn.ReviewSelf(ctx, c.dyn)
	if err != nil {
		return fmt.Errorf("asking the hub who you are: %w", err)
	}
	c.user = me.User
	allowed, err := c.opts.Authorize(ctx, authn.Person{User: me.User}, "", res)
	switch {
	case apierrors.IsForbidden(err):
		_, _ = fmt.Fprintf(c.cmd.ErrOrStderr(), "Pacto could not ask whether %s itself may approve (%v); the record counts only if that user, without your groups, may\n", me.User, err)
	case err != nil:
		return fmt.Errorf("asking the hub whether %s may approve: %w", me.User, err)
	case !allowed:
		return fmt.Errorf("ClusterApproveNeedsUserGrant: approve must be granted to your user %s; groups are not checked because a certificate names none", me.User)
	}
	return nil
}

// login logs the person in to sign what on subject and checks the login
// signs as the user the hub reviewed.
func (c *clusterDecision) login(what, subject string) (delivery.Signer, error) {
	s, email, err := keylessLogin(c.cmd, c.v, c.d, c.signer, what, subject)
	if err != nil {
		return nil, err
	}
	id, source, err := delivery.ApproverIdentity(c.signer.Issuer, email, c.policy)
	if err != nil {
		return nil, err
	}
	if code, why := delivery.KeylessRefusal(id, source); code != "" {
		return nil, errors.New(code + ": " + why)
	}
	if id.User != c.user {
		return nil, fmt.Errorf("ApproverMismatch: your Kubernetes user is %s and your login signs as %s; nothing was signed", c.user, id.User)
	}
	return s, nil
}

// sign logs the person in and writes rec signed by them.
func (c *clusterDecision) sign(rec delivery.ClusterJoin) error {
	what := "an approval"
	if c.dec == delivery.JoinRemove {
		what = "a removal"
	}
	s, err := c.login(what, "cluster "+rec.Cluster)
	if err != nil {
		return err
	}
	ref, mode, err := c.svc.WriteClusterJoin(c.cmd.Context(), rec, s, c.opts.Repository)
	if err != nil {
		return err
	}
	verb, name := "approved", c.member.Name
	if c.dec == delivery.JoinRemove {
		verb = "removed"
	}
	if rec.Name != "" {
		name = rec.Name
	}
	_, _ = fmt.Fprintf(c.cmd.OutOrStdout(), "%s %s (%s) as %s; recorded %s\n", verb, name, shortUID(rec.Cluster), c.user, ref)
	c.printTag(ref, mode, deliveryrecord.KindJoin)
	return nil
}

// printTag prints the write-once tag a record of kind at ref sits under,
// when the registry has no Referrers.
func (c *clusterDecision) printTag(ref string, mode capabilities.ReferrersMode, kind string) {
	if mode == capabilities.ReferrersWriteOnceTags {
		_, dg, _ := strings.Cut(ref, "@")
		_, _ = fmt.Fprintf(c.cmd.OutOrStdout(), "pushed %s:%s (write-once tag; registry has no Referrers)\n", c.opts.Repository,
			deliveryrecord.KindTag(delivery.ClusterJoinStream(c.member.UID), kind, digest.Digest(dg)))
	}
}
