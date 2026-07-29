package main

// Register the cloud blob drivers so `pacto evidence serve --bucket-url` accepts
// s3://, gs:// and azblob:// at runtime, not just the default file://. The
// evidence store speaks the Go CDK's driver-neutral API, so the same evidence
// logic runs over any of these when a cloud bucket URL is requested — no code
// change, only a driver registration. Credentials come from the standard cloud
// SDK environment, never from Pacto flags.
import (
	_ "gocloud.dev/blob/azureblob"
	_ "gocloud.dev/blob/gcsblob"
	_ "gocloud.dev/blob/s3blob"
)
