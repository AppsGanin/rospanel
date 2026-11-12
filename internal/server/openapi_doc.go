package server

import (
	"bytes"
	"encoding/json"
)

// OpenAPIDocument is the spec as docs/openapi.json holds it: two-space indent, a final
// newline, and no HTML escaping. That is byte for byte what release-please writes when
// it bumps info.version in the file, so a release leaves the snapshot current.
func OpenAPIDocument(serverURL string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(OpenAPISpec(serverURL)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
