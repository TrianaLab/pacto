package app

import (
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/pkg/contractview"
	"github.com/trianalab/pacto/v4/pkg/doc"
	"github.com/trianalab/pacto/v4/pkg/graph"
	"github.com/trianalab/pacto/v4/pkg/lock"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// Service is the application service container. It holds injected dependencies
// and provides methods for each CLI command.
type Service struct {
	BundleStore  oci.BundleStore
	PluginRunner PluginRunner
	// Version is the running CLI version, recorded into pacto.lock provenance.
	// Empty reads as "dev".
	Version string

	// sys holds the side effects the methods reach for; read it through env.
	sys sysDeps
}

// sysDeps are the clock, filesystem, entropy and network the Service touches. A
// nil field is the production default, so a Service built as a struct literal
// behaves like one from NewService; a test sets fields on its own Service.
type sysDeps struct {
	writeFile   func(name string, data []byte, perm os.FileMode) error
	mkdirTemp   func(dir, pattern string) (string, error)
	absPath     func(path string) (string, error)
	now         func() time.Time
	rand        io.Reader
	generateDoc func(*contractview.ServiceDetails, *graph.Result) (string, error)
	marshalLock func(*lock.Lock) ([]byte, error)
	newK8s      func() (k8sclient.K8sClient, error)
	kubeContext func() string
	// readinessTimeout bounds ONE Evidence Server readiness preflight; zero is
	// defaultReadinessTimeout.
	readinessTimeout time.Duration
	// httpClient posts evidence envelopes.
	httpClient *http.Client
	// run runs the program publish hands work to (docker buildx).
	run runner
	// lookPath finds the program a publish build runs.
	lookPath func(file string) (string, error)
	// publishRegistry is the registry publish reads and writes.
	publishRegistry func() publish.Registry
	// refusalRepo opens the repository a publish refusal is kept in.
	refusalRepo func(path string) (*remote.Repository, error)
}

// env is sys with every unset field at its production default.
func (s *Service) env() sysDeps {
	d := s.sys
	if d.writeFile == nil {
		d.writeFile = os.WriteFile
	}
	if d.mkdirTemp == nil {
		d.mkdirTemp = os.MkdirTemp
	}
	if d.absPath == nil {
		d.absPath = filepath.Abs
	}
	if d.now == nil {
		d.now = time.Now
	}
	if d.rand == nil {
		d.rand = rand.Reader
	}
	if d.generateDoc == nil {
		d.generateDoc = doc.Generate
	}
	if d.marshalLock == nil {
		d.marshalLock = (*lock.Lock).Marshal
	}
	if d.newK8s == nil {
		d.newK8s = k8sclient.NewGoClient
	}
	if d.kubeContext == nil {
		d.kubeContext = k8sclient.CurrentKubeContext
	}
	if d.readinessTimeout == 0 {
		d.readinessTimeout = defaultReadinessTimeout
	}
	if d.httpClient == nil {
		d.httpClient = evidenceClient
	}
	publishDefaults(&d)
	return d
}

// publishDefaults sets the publish path's unset fields of d to their
// production defaults.
func publishDefaults(d *sysDeps) {
	if d.run == nil {
		d.run = execRun
	}
	if d.lookPath == nil {
		d.lookPath = exec.LookPath
	}
	if d.publishRegistry == nil {
		d.publishRegistry = defaultPublishRegistry
	}
	if d.refusalRepo == nil {
		d.refusalRepo = defaultRefusalRepo
	}
}

// NewService creates a new application service with the given dependencies.
func NewService(store oci.BundleStore, pluginRunner PluginRunner) *Service {
	return &Service{
		BundleStore:  store,
		PluginRunner: pluginRunner,
	}
}

// requireBundleStore returns an error if the BundleStore is not configured.
func (s *Service) requireBundleStore() error {
	if s.BundleStore == nil {
		return fmt.Errorf("OCI registry client not configured")
	}
	return nil
}
