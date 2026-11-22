package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
)

func newClusterMapGroupsCommand(svc *app.Service, v *viper.Viper, d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "map-groups <uid>",
		Short: "Approve the group mapping a member uses for people of another issuer, signed as you",
		Long: "When a member's apiserver does not accept the hub's tokens, because its OIDC issuer differs or the hub exchanges none, the hub reviews a " +
			"signed-in person in the member as a mapped identity: their user, bound to their issuer, with the member groups this mapping gives their hub groups. " +
			"A mapping answers observation reads only, never approvals. This command signs the mapping as you, keyless after a fresh login in your browser, " +
			"and records it beside the member's snapshot; the newest mapping you sign for an issuer replaces the one before. --clear signs a mapping with no " +
			"rules, which removes it: for --issuer, or for every issuer the member has a mapping for. Member groups that start with system: are refused. " +
			"You need approve on clusterjoins.pacto.trianalab.io in the hub, granted to your user (" + clusterGrant + ").",
		Example: "  pacto cluster map-groups 9f1c4e2a --issuer https://login.acme.dev --map platform=pacto-readers --registry ghcr.io/acme\n" +
			"  pacto cluster map-groups 9f1c4e2a --clear --registry ghcr.io/acme",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return mapGroups(&clusterDecision{cmd: cmd, svc: svc, v: v, d: d}, args[0])
		},
	}
	addClusterHubFlags(cmd)
	cmd.Flags().String("issuer", "", "the OIDC issuer whose people the mapping maps, an https URL (required unless --clear)")
	cmd.Flags().StringArray("map", nil, "a rule <hub group>=<member group>; repeat it for each rule")
	cmd.Flags().Bool("clear", false, "remove the mapping of --issuer, or of every issuer when --issuer is not set")
	cmd.Flags().String("signer-issuer", "", "the approver signer's OIDC issuer, when the hub lists several")
	return cmd
}

// mapGroups signs the group mapping the flags name for the member uid names
// as the person and records it: every check that can refuse runs before the
// login.
func mapGroups(c *clusterDecision, uid string) error {
	issuer, rules, err := mappingFlags(c.cmd)
	if err != nil {
		return err
	}
	signerIssuer, _ := c.cmd.Flags().GetString("signer-issuer")
	if c.dyn, c.opts, err = clusterHub(c.cmd, c.d); err != nil {
		return err
	}
	if fullUID.MatchString(uid) {
		c.opts.Include = uid
	}
	if c.member, err = findCluster(c.svc.ClusterJoins(c.cmd.Context(), c.opts), uid); err != nil {
		return err
	}
	if c.member.State != delivery.JoinApproved {
		return fmt.Errorf("%s (%s) is %s: groups are mapped for an approved member", clusterName(c.member), shortUID(c.member.UID), c.member.State)
	}
	if err := c.pickSigner(signerIssuer); err != nil {
		return err
	}
	if err := c.authority(); err != nil {
		return err
	}
	recs, err := mappingRecords(c.member, c.opts.Hub, c.user, issuer, rules)
	if err != nil {
		return err
	}
	if len(recs) == 0 {
		_, _ = fmt.Fprintf(c.cmd.OutOrStdout(), "%s (%s) has no group mapping to clear; nothing was signed\n", clusterName(c.member), shortUID(c.member.UID))
		return nil
	}
	return c.signMappings(recs)
}

// mappingFlags are the issuer and rules cmd's flags name; no rules is a
// --clear.
func mappingFlags(cmd *cobra.Command) (string, []delivery.GroupRule, error) {
	issuer, _ := cmd.Flags().GetString("issuer")
	pairs, _ := cmd.Flags().GetStringArray("map")
	clear, _ := cmd.Flags().GetBool("clear")
	rules, err := groupRules(pairs)
	switch {
	case err != nil:
		return "", nil, err
	case clear && len(rules) > 0:
		return "", nil, errors.New("--clear takes no --map")
	case !clear && len(rules) == 0:
		return "", nil, errors.New("name at least one --map <hub group>=<member group>, or --clear")
	case !clear && issuer == "":
		return "", nil, errors.New("--issuer is required: the OIDC issuer whose people the mapping maps")
	}
	return issuer, rules, nil
}

// groupRules parses --map pairs.
func groupRules(pairs []string) ([]delivery.GroupRule, error) {
	var out []delivery.GroupRule
	for _, p := range pairs {
		from, to, ok := strings.Cut(p, "=")
		if !ok || from == "" || to == "" {
			return nil, fmt.Errorf("--map %q wants <hub group>=<member group>", p)
		}
		out = append(out, delivery.GroupRule{From: from, To: to})
	}
	return out, nil
}

// mappingRecords are the records to sign for member as user: one for issuer
// with rules, or with no rules a clear of issuer, or of every issuer the
// member has a mapping for when issuer is "". Each is validated.
func mappingRecords(member delivery.ClusterJoinState, hub, user, issuer string, rules []delivery.GroupRule) ([]delivery.ClusterGroupMapping, error) {
	issuers := []string{issuer}
	if issuer == "" {
		issuers = nil
		for _, m := range member.Mappings {
			issuers = append(issuers, m.Issuer)
		}
	}
	at := time.Now().UTC().Format(time.RFC3339)
	var out []delivery.ClusterGroupMapping
	for _, iss := range issuers {
		rec := delivery.ClusterGroupMapping{Cluster: member.UID, Hub: hub, Issuer: iss, Rules: rules, Approver: user, At: at}
		if err := rec.Validate(); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

// signMappings logs the person in once and writes recs signed by them.
func (c *clusterDecision) signMappings(recs []delivery.ClusterGroupMapping) error {
	s, err := c.login("a group mapping", "cluster "+c.member.UID)
	if err != nil {
		return err
	}
	for _, rec := range recs {
		ref, mode, err := c.svc.WriteClusterGroupMapping(c.cmd.Context(), rec, s, c.opts.Repository)
		if err != nil {
			return err
		}
		verb := fmt.Sprintf("mapped %d groups of", len(rec.Rules))
		if len(rec.Rules) == 0 {
			verb = "cleared the group mapping of"
		}
		_, _ = fmt.Fprintf(c.cmd.OutOrStdout(), "%s %s (%s) for %s as %s; recorded %s\n", verb, clusterName(c.member), shortUID(rec.Cluster), rec.Issuer, c.user, ref)
		c.printTag(ref, mode, deliveryrecord.KindGroupMapping)
	}
	return nil
}

// mappingWords say who approved each of c's group mappings, "" for none.
func mappingWords(c delivery.ClusterJoinState) string {
	var out []string
	for _, m := range c.Mappings {
		out = append(out, fmt.Sprintf("%s %s (%s) for %s", m.ApprovedBy, m.ApprovedAt[:min(len(m.ApprovedAt), len(time.DateOnly))], ruleCount(len(m.Rules)), m.Issuer))
	}
	return strings.Join(out, "; ")
}

// ruleCount is "1 rule" or "n rules".
func ruleCount(n int) string {
	if n == 1 {
		return "1 rule"
	}
	return fmt.Sprintf("%d rules", n)
}
