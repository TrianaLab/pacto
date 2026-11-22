package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// printExternalHeader writes the header of an external answer: the system,
// how many consumers each kind of source names, the change of its inferred
// contract and the gate.
func printExternalHeader(w io.Writer, result *impact.Result) {
	x := result.External
	what := "external"
	if x.Provider != "" {
		what = "inferred external"
	}
	if x.Class != "" {
		what += ", " + x.Class
	}
	_, _ = fmt.Fprintf(w, "Event: %s %s (%s): %s  asOf %s  completeness %s\n", x.Identity, result.Event.Kind, what,
		externalCounts(result.Consumers), result.AsOf.Format(time.RFC3339), result.Completeness)
	if x.Change != nil {
		_, _ = fmt.Fprintln(w, "changed: "+externalChangeText(x.Change))
	}
	why := "an external system is never verified"
	if x.Provider != "" {
		why = "provider is inferred"
	}
	_, _ = fmt.Fprintf(w, "gate: %s (%s)\n", result.Gate, why)
}

// externalCounts says how many consumers an external answer lists and which
// kind of source names each: declared, observed only or inferred only.
func externalCounts(consumers []impact.AffectedConsumer) string {
	declared, observed := 0, 0
	for _, c := range consumers {
		switch {
		case slices.Contains(c.Sources, "declared"):
			declared++
		case slices.Contains(c.Sources, fleet.ExternalSourceObserved):
			observed++
		}
	}
	noun := "consumers"
	if len(consumers) == 1 {
		noun = "consumer"
	}
	text := fmt.Sprintf("%d %s, %d declared, %d observed only", len(consumers), noun, declared, observed)
	if inferred := len(consumers) - declared - observed; inferred > 0 {
		text += fmt.Sprintf(", %d inferred only", inferred)
	}
	return text
}

// externalChangeText says what changed between the two inferred revisions:
// the versions, the vendor OpenAPI digest, the surface values added (+) and
// removed (-), and whether the pair's signatures were checked.
func externalChangeText(c *fleet.ExternalProviderChange) string {
	parts := []string{c.From + " -> " + c.To}
	if c.OpenAPIFrom != "" || c.OpenAPITo != "" {
		parts = append(parts, "openapi "+openAPIDigestText(c.OpenAPIFrom)+" -> "+openAPIDigestText(c.OpenAPITo))
	}
	for _, sign := range []struct {
		mark   string
		values []string
	}{{"+", c.Added}, {"-", c.Removed}} {
		for _, v := range sign.values {
			list, value, _ := strings.Cut(v, " ")
			parts = append(parts, list+" "+sign.mark+value)
		}
	}
	if !c.Checked {
		parts = append(parts, "(unchecked: this installation's operator did not sign both revisions)")
	}
	return strings.Join(parts, "  ")
}

// openAPIDigestText is the short digest of a vendor OpenAPI link, none for
// no link.
func openAPIDigestText(link string) string {
	if link == "" {
		return "none"
	}
	return shortDigest(fleet.ExternalOpenAPIDigest(link))
}

// printExternalConsumers writes the consumers of one lane of an external
// answer as a table: where each use was seen, the client major its record
// names and the range it pins.
func printExternalConsumers(w io.Writer, result *impact.Result, in []impact.AffectedConsumer) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "  CONSUMER\tSOURCE\tCLIENT MAJOR\tRANGE\tOUTCOME")
	for _, c := range in {
		_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", c.Service, orDash(strings.Join(c.Sources, ", ")), externalClientText(c.Client),
			externalRangeText(result, c), c.Effect)
	}
	_ = tw.Flush()
}

// externalClientText is a consumer's client major, - when its record names
// none: a missing major is never guessed.
func externalClientText(c *fleet.ExternalClient) string {
	if c == nil {
		return "-"
	}
	return fmt.Sprintf("%s v%d (%s)", c.SDK, c.Major, c.Confidence)
}

// externalRangeText is the range a consumer pins on the external system and
// its verdict; unknown when it pins none.
func externalRangeText(result *impact.Result, c impact.AffectedConsumer) string {
	switch {
	case c.Compatibility == "" || c.Compatibility == contract.CompatibilityUnknown:
		return contract.CompatibilityUnknown
	case result.External.Provider != "":
		return c.Compatibility + " (" + c.CompatibilityVerdict + ": " + impact.LimitationInferredProvider + ")"
	}
	return c.Compatibility + " (" + c.CompatibilityVerdict + ")"
}

// printExternalPin writes the dependencies block an external answer
// suggests: a range on its inferred contract, or the declaration that names
// the external when there is none to pin a range on.
func printExternalPin(w io.Writer, result *impact.Result) {
	if result.Pin == "" {
		return
	}
	title := "declare it:"
	if result.External.Provider != "" {
		title = "pin a range:"
	}
	_, _ = fmt.Fprintln(w, title)
	for _, l := range strings.Split(strings.TrimRight(result.Pin, "\n"), "\n") {
		_, _ = fmt.Fprintln(w, "  "+l)
	}
}
