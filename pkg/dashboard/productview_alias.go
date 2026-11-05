package dashboard

import "github.com/trianalab/pacto/v4/pkg/dashboard/productview"

// The product-transport DTOs and their route builder live in the leaf package
// pkg/dashboard/productview so the MCP server can decorate its fleet answers with
// the same hrefs without linking this server. They are aliased back here, like the
// pkg/contractview DTOs, so the handlers and the published OpenAPI component names
// stay exactly as they were.
type (
	ProductRef              = productview.ProductRef
	ProductRefPreview       = productview.ProductRefPreview
	ProductRefResolution    = productview.ProductRefResolution
	ProductOverview         = productview.ProductOverview
	ProductEntityList       = productview.ProductEntityList
	ProductEntityDetail     = productview.ProductEntityDetail
	ProductRevisionDocument = productview.ProductRevisionDocument
	ProductNeighborhood     = productview.ProductNeighborhood
	ProductAttentionList    = productview.ProductAttentionList
)
