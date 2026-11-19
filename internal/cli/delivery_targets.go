package cli

import (
	"cmp"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/signing"
	"github.com/trianalab/pacto/v4/pkg/strictjson"
)

// targetsPublished is what `pacto delivery targets publish` pushed.
type targetsPublished struct {
	Ref          string `json:"ref"`
	Cluster      string `json:"cluster"`
	AsOf         string `json:"asOf"`
	Key          string `json:"key"`
	RegistryMode string `json:"registryMode,omitempty"`
}

func newDeliveryTargetsCommand(v *viper.Viper, d deps) *cobra.Command {
	cmd := &cobra.Command{Use: "targets", Short: "Work with the signed target snapshots CI reads without cluster credentials"}
	publish := &cobra.Command{
		Use:   "publish <file>",
		Short: "Sign a target snapshot document and push it to <registry>/" + delivery.TargetSnapshotRepository,
		Long: "Signs a target snapshot document and pushes it to <registry>/" + delivery.TargetSnapshotRepository + ":" +
			delivery.TargetSnapshotTagPrefix + "<cluster>. The document is the JSON predicate of " + delivery.PredicateTargetSnapshot +
			": the cluster UID, its completeness and limitations, the Pacto objects as the read contract and the workloads no Pacto " +
			"targets. The command sets asOf to now, the publisher to the signing key and the registry mode to the one detected on " +
			"the cluster's existing tag.\n\n" +
			"Production clusters publish from the delivery recorder. This command is for demos, tests and installations that build " +
			"the document themselves.\n\n" +
			"--key takes a PEM private key or a `pacto evidence keygen` seed. After a push the command writes <key>.pub.pem when it " +
			"is absent; it refuses to publish while that file holds another key. A reader verifies the snapshot only with a trust entry that holds that public key and is scoped to the " +
			"cluster (streams: [\"<cluster>/*/*\"]).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			registry, _ := cmd.Flags().GetString("registry")
			keyPath, _ := cmd.Flags().GetString("key")
			res, err := publishTargets(cmd, d, args[0], registry, keyPath)
			if err != nil {
				return err
			}
			return formatResult(cmd, v.GetString(outputFormatKey), res, func() error {
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "Published %s (cluster %s, asOf %s, key %s)\n", res.Ref, res.Cluster, res.AsOf, res.Key)
				return err
			}, nil)
		},
	}
	publish.Flags().String("registry", "", "registry host and path the snapshot is written under, as <registry>/"+delivery.TargetSnapshotRepository)
	publish.Flags().String("key", "", "the signing key: a PEM private key or a seed that pacto evidence keygen wrote")
	_ = publish.MarkFlagRequired("registry")
	_ = publish.MarkFlagRequired("key")
	cmd.AddCommand(publish)
	return cmd
}

// publishTargets signs the snapshot document at file with the key at keyPath
// and pushes it under registry.
func publishTargets(cmd *cobra.Command, d deps, file, registry, keyPath string) (targetsPublished, error) {
	s, err := readTargetSnapshot(file)
	if err != nil {
		return targetsPublished{}, err
	}
	kp, err := loadTargetsKey(keyPath)
	if err != nil {
		return targetsPublished{}, err
	}
	fingerprint := signing.Hint(kp.GetPublicKey())
	s.AsOf, s.Publisher = time.Now().UTC().Format(time.RFC3339), "key:"+fingerprint
	// The sections are signed as written; one with no asOf was read now.
	for i := range s.Sources {
		s.Sources[i].AsOf = cmp.Or(s.Sources[i].AsOf, s.AsOf)
	}
	if err := s.Check(); err != nil {
		return targetsPublished{}, fmt.Errorf("%s: %w", file, err)
	}
	if err := checkPublicKeyFile(keyPath+".pub.pem", fingerprint); err != nil {
		return targetsPublished{}, err
	}
	path := strings.TrimSuffix(contract.TrimOCIScheme(registry), "/") + "/" + delivery.TargetSnapshotRepository
	ref, err := pushTargets(cmd.Context(), d, path, &s, signing.Keyed(kp, ""))
	if err != nil {
		return targetsPublished{}, err
	}
	if err := writePublicKeyOnce(keyPath+".pub.pem", kp); err != nil {
		return targetsPublished{}, err
	}
	return targetsPublished{Ref: ref, Cluster: s.Cluster, AsOf: s.AsOf, Key: fingerprint, RegistryMode: s.RegistryMode}, nil
}

// pushTargets records the registry mode of the cluster's existing tag in s,
// signs s with signer and pushes it to the repository at path. It returns the
// pushed <path>@<digest>.
func pushTargets(ctx context.Context, d deps, path string, s *delivery.TargetSnapshot, signer delivery.Signer) (string, error) {
	repo, err := d.newRecordRepo(path)
	if err != nil {
		return "", err
	}
	tag, _ := delivery.TargetSnapshotTag(s.Cluster) // Check accepted the cluster
	// Only a detected mode is signed, never the one the document claims.
	s.RegistryMode = ""
	if _, err := repo.Resolve(ctx, tag); err == nil {
		// The probe needs a subject that exists: the first publish records no mode.
		if caps, err := app.NewRegistryProbe()(ctx, contract.OCIScheme+path+":"+tag); err == nil {
			s.RegistryMode = caps.Mode()
		}
	}
	bundle, err := delivery.SignTargetSnapshot(ctx, signer, delivery.NewTargetSnapshotStatement(*s))
	if err != nil {
		return "", err
	}
	desc, err := deliveryrecord.PushTargetSnapshot(ctx, repo, s.Cluster, bundle)
	if err != nil {
		return "", err
	}
	return path + "@" + desc.Digest.String(), nil
}

// readTargetSnapshot reads a snapshot document, bounded and strict.
func readTargetSnapshot(file string) (delivery.TargetSnapshot, error) {
	f, err := os.Open(file)
	if err != nil {
		return delivery.TargetSnapshot{}, err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, delivery.MaxTargetSnapshotBytes+1))
	if err != nil {
		return delivery.TargetSnapshot{}, err
	}
	if len(raw) > delivery.MaxTargetSnapshotBytes {
		return delivery.TargetSnapshot{}, fmt.Errorf("%s: over the %d byte bound", file, delivery.MaxTargetSnapshotBytes)
	}
	var s delivery.TargetSnapshot
	if err := strictjson.Unmarshal(raw, &s); err != nil {
		return delivery.TargetSnapshot{}, fmt.Errorf("%s: %w", file, err)
	}
	return s, nil
}

// loadTargetsKey reads a PEM private key or, when the file is none, a
// `pacto evidence keygen` seed (base64 of 32 bytes).
func loadTargetsKey(path string) (*signing.Keypair, error) {
	kp, pemErr := signing.LoadKey(path)
	if pemErr == nil {
		return kp, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, pemErr
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("%w; it is no pacto evidence keygen seed either", pemErr)
	}
	return signing.FromPrivateKey(ed25519.NewKeyFromSeed(seed))
}

// checkPublicKeyFile refuses a file at path that holds another public key
// than the one with fingerprint, so <key>.pub.pem always names the key that
// signs (a seed regenerated in place would otherwise keep the old one).
func checkPublicKeyFile(path, fingerprint string) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	held := "no PEM public key"
	if block, _ := pem.Decode(raw); block != nil {
		if pub, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
			if signing.Hint(pub) == fingerprint {
				return nil
			}
			held = "key " + signing.Hint(pub)
		}
	}
	return fmt.Errorf("%s holds %s, not the signing key %s: remove it so the publish writes the new one", path, held, fingerprint)
}

// writePublicKeyOnce writes kp's PEM public key to path unless a file is there.
func writePublicKeyOnce(path string, kp *signing.Keypair) error {
	pub, _ := delivery.PublicKeyPEM(kp.GetPublicKey()) // a key signing.LoadKey or a seed made always marshals
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = f.WriteString(pub)
	return errors.Join(err, f.Close())
}
