package apispec

import _ "embed"

// OpenAPI is served directly by the API so documentation stays available in
// the minimal production image without a writable filesystem.
//
//go:embed openapi.yaml
var OpenAPI []byte
