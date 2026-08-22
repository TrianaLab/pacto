package cli

import (
	"fmt"
	"net"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v3/internal/app"
)

// newEvidenceCommand builds the `pacto evidence` command group: produce, verify,
// serve and send signed evidence envelopes. keygen mints an Ed25519 signing
// keypair, sign wraps an EvidenceSet in a signed envelope, verify checks one
// against a trust store, serve runs the ingestion host and send posts an
// envelope to one.
func newEvidenceCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "evidence",
		Short: "Sign, verify, serve and send external evidence envelopes",
		Long: "Produce and verify the signed, versioned envelopes that carry a Pacto " +
			"EvidenceSet from a remote or disconnected environment to a platform that " +
			"ingests it. Keys are Ed25519; the wire format is defined by pkg/evidenceenvelope.",
	}
	cmd.AddCommand(newEvidenceKeygenCommand(svc, v))
	cmd.AddCommand(newEvidenceSignCommand(svc, v))
	cmd.AddCommand(newEvidenceVerifyCommand(svc, v))
	cmd.AddCommand(newEvidenceServeCommand(svc, v))
	cmd.AddCommand(newEvidenceSendCommand(svc, v))
	return cmd
}

func newEvidenceServeCommand(svc *app.Service, _ *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the evidence ingestion host",
		Long: "Starts an HTTP host that accepts signed evidence envelopes at " +
			"POST /api/evidence/v1/envelopes, verifies them against --trust, evaluates " +
			"the carried evidence against its resolved contract and publishes accepted " +
			"records to the contract registry as OCI 1.1 referrers of the exact contract " +
			"revision each report is about. Every --subject is an immutable " +
			"oci://<repo>@sha256:<digest> reference; the registry is the only durable " +
			"store, so the host keeps no local state and survives restarts. GET " +
			".../health is an always-200 liveness probe; .../ready reports 503 while a " +
			"subject cannot be resolved or its referrers enumerated; .../producers " +
			"advertises trusted producer ids; .../targets exposes the latest accepted " +
			"targets. Registry credentials come from the same sources as `pacto pull`. " +
			"Exactly one server may write to a subject set. Serves until interrupted.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := app.ServeOptions{}
			opts.TrustPath, _ = cmd.Flags().GetString("trust")
			opts.Subjects, _ = cmd.Flags().GetStringArray("subject")
			opts.Producers, _ = cmd.Flags().GetStringArray("producer")
			addr := listenAddress(cmd)
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "evidence ingestion host listening on http://%s\n", addr)
			return svc.ServeEvidenceOnListener(cmd.Context(), ln, opts)
		},
	}
	cmd.Flags().Int("port", 8686, "port to listen on (127.0.0.1); superseded by --listen-address")
	cmd.Flags().String("listen-address", "", "host:port to listen on (supersedes --port)")
	cmd.Flags().String("trust", "", "trust store: a public-key file or a directory of <keyId>.pub files")
	cmd.Flags().StringArray("subject", nil, "exact contract revision evidence is stored on: oci://<repo>@sha256:<digest> (repeatable, required)")
	cmd.Flags().StringArray("producer", nil, "advertised trusted producer id (repeatable)")
	_ = cmd.MarkFlagRequired("trust")
	_ = cmd.MarkFlagRequired("subject")
	return cmd
}

// listenAddress resolves the serve bind address: --listen-address wins; else
// 127.0.0.1:<port> keeps --port working.
func listenAddress(cmd *cobra.Command) string {
	if addr, _ := cmd.Flags().GetString("listen-address"); addr != "" {
		return addr
	}
	port, _ := cmd.Flags().GetInt("port")
	return fmt.Sprintf("127.0.0.1:%d", port)
}

func newEvidenceSendCommand(svc *app.Service, _ *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "send <envelope.json>",
		Short: "Post a signed envelope to an ingestion host",
		Long: "Reads a signed envelope JSON file and POSTs it to an ingestion host's " +
			"--url. Prints the host's JSON response and exits non-zero on a non-2xx status.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			url, _ := cmd.Flags().GetString("url")
			res, err := svc.SendEvidence(cmd.Context(), url, args[0])
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Body)
			if res.StatusCode < 200 || res.StatusCode >= 300 {
				return fmt.Errorf("ingestion host returned status %d", res.StatusCode)
			}
			return nil
		},
	}
	cmd.Flags().String("url", "", "ingestion host envelope endpoint URL")
	_ = cmd.MarkFlagRequired("url")
	return cmd
}

func newEvidenceKeygenCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "keygen",
		Short: "Generate an Ed25519 signing keypair",
		Long: "Writes the private seed to <keyId>.key (base64, 0600) and the public key " +
			"into --out. With --producer, the public key is written as " +
			"<producer>__<keyId>.pub, which binds the key to that producer in the trust " +
			"store — hand that file to the platform. Sign with the SAME --producer and " +
			"--key-id. With no --producer, a bare <keyId>.pub binds the key to a producer " +
			"named after the key id (the single-producer default).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, _ := cmd.Flags().GetString("out")
			keyID, _ := cmd.Flags().GetString("key-id")
			producer, _ := cmd.Flags().GetString("producer")
			force, _ := cmd.Flags().GetBool("force")
			kp, err := svc.GenerateKey(dir, producer, keyID, force)
			if err != nil {
				return err
			}
			return printEvidenceKeygen(cmd, kp, v.GetString(outputFormatKey))
		},
	}
	cmd.Flags().String("out", ".", "directory to write the keypair into")
	cmd.Flags().String("key-id", "", "key id (defaults to a fingerprint of the public key)")
	cmd.Flags().String("producer", "", "producer id to bind the key to (writes <producer>__<keyId>.pub)")
	cmd.Flags().Bool("force", false, "overwrite existing key files instead of failing")
	return cmd
}

func newEvidenceSignCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sign <evidence.json>",
		Short: "Sign an EvidenceSet into a signed envelope",
		Long: "Reads an EvidenceSet JSON file, wraps it in an Ed25519-signed envelope and " +
			"prints the envelope JSON. The id defaults to a content hash of the evidence; " +
			"pass --id and --issued-at for a fully deterministic envelope.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := app.SignOptions{EvidencePath: args[0]}
			opts.KeyPath, _ = cmd.Flags().GetString("key")
			opts.KeyID, _ = cmd.Flags().GetString("key-id")
			opts.ProducerID, _ = cmd.Flags().GetString("producer")
			opts.ProducerVersion, _ = cmd.Flags().GetString("producer-version")
			opts.ID, _ = cmd.Flags().GetString("id")
			seq, _ := cmd.Flags().GetUint64("sequence")
			opts.Sequence = seq
			opts.TTL, _ = cmd.Flags().GetDuration("ttl")
			if issued, _ := cmd.Flags().GetString("issued-at"); issued != "" {
				t, err := time.Parse(time.RFC3339, issued)
				if err != nil {
					return fmt.Errorf("invalid --issued-at: %w", err)
				}
				opts.IssuedAt = t
			}
			env, err := svc.SignEvidence(opts)
			if err != nil {
				return err
			}
			// The signed envelope is a wire artifact — always emit exact JSON so it
			// stays verifiable regardless of --output-format.
			return printJSON(cmd, env)
		},
	}
	cmd.Flags().String("key", "", "path to the private key file")
	cmd.Flags().String("key-id", "", "producer key id (must match the trust-store entry)")
	cmd.Flags().String("producer", "", "producer id")
	cmd.Flags().String("producer-version", "", "producer version (optional)")
	cmd.Flags().String("id", "", "envelope id (defaults to a content hash of the evidence)")
	cmd.Flags().Uint64("sequence", 0, "producer-scoped monotonic sequence; each report must be strictly greater than the producer's last")
	cmd.Flags().String("issued-at", "", "issued-at timestamp (RFC3339; defaults to now)")
	cmd.Flags().Duration("ttl", 24*time.Hour, "validity window; 0 disables expiry")
	_ = cmd.MarkFlagRequired("key")
	return cmd
}

func newEvidenceVerifyCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify <envelope.json>",
		Short: "Verify a signed evidence envelope against a trust store",
		Long: "Decodes an envelope and verifies its signature, freshness and trust against " +
			"a --trust public-key file or directory of <keyId>.pub files. Exits non-zero " +
			"when verification fails.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			trust, _ := cmd.Flags().GetString("trust")
			format := v.GetString(outputFormatKey)
			result, err := svc.VerifyEnvelope(app.VerifyOptions{EnvelopePath: args[0], TrustPath: trust})
			if err != nil {
				return err
			}
			if err := printEvidenceVerify(cmd, result, format); err != nil {
				return err
			}
			if !result.OK {
				return fmt.Errorf("verification failed: %s", result.Reason)
			}
			return nil
		},
	}
	cmd.Flags().String("trust", "", "trust store: a public-key file or a directory of <keyId>.pub files")
	_ = cmd.MarkFlagRequired("trust")
	return cmd
}

func printEvidenceKeygen(cmd *cobra.Command, kp app.KeyPair, format string) error {
	return formatResult(cmd, format, kp, func() error {
		w := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(w, "key id:      %s\n", kp.KeyID)
		_, _ = fmt.Fprintf(w, "private key: %s\n", kp.PrivateKeyPath)
		_, _ = fmt.Fprintf(w, "public key:  %s\n", kp.PublicKeyPath)
		return nil
	}, nil)
}

func printEvidenceVerify(cmd *cobra.Command, r app.VerifyResult, format string) error {
	return formatResult(cmd, format, r, func() error {
		w := cmd.OutOrStdout()
		if r.OK {
			_, _ = fmt.Fprintf(w, "ok: envelope %s from %s (key %s) is valid\n", r.ID, r.Producer, r.KeyID)
		} else {
			_, _ = fmt.Fprintf(w, "FAILED: %s\n", r.Reason)
		}
		return nil
	}, nil)
}
