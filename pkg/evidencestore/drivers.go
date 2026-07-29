package evidencestore

// Blank-import the local blob drivers so file:// and mem:// URLs resolve without
// the caller wiring them. Cloud drivers (s3, gs, azblob) are imported by the
// service binary, never here — this package stays cloud-account free.
import (
	_ "gocloud.dev/blob/fileblob"
	_ "gocloud.dev/blob/memblob"
)
