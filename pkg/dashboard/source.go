package dashboard

import "context"

// DataSource is the core abstraction for loading service data into the dashboard.
// Implementations exist for Kubernetes (CRD status), OCI registries, and local filesystem.
type DataSource interface {
	ListServices(ctx context.Context) ([]Service, error)
	GetService(ctx context.Context, name string) (*ServiceDetails, error)
	GetVersions(ctx context.Context, name string) ([]Version, error)
	GetDiff(ctx context.Context, a, b Ref) (*DiffResult, error)
}
