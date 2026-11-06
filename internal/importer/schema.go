package importer

import _ "embed"

// RetainV1Schema contains the embedded JSON Schema for the retain/v1 format.
//
//go:embed retain-v1.schema.json
var RetainV1Schema []byte
