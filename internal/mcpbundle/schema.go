package mcpbundle

import (
	"crypto/sha256"
	_ "embed"
	jsonv2 "encoding/json/v2"
	"fmt"
	"sync"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	manifestSchemaResource = "https://raw.githubusercontent.com/modelcontextprotocol/mcpb/2a788100a60db19a6b1c018fb1cf84ae85de9537/schemas/mcpb-manifest-v0.4.schema.json"
	serverSchemaResource   = "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json"
	manifestSchemaSHA256   = "9e4fa3cdc4ae3872b3d76dd538a2517c4e9cf43a7ea2707819e11aedce09ee69"
	serverSchemaSHA256     = "578b5bb01866d060ff6a67734cf6b2f17a5da283a0877775c7913e4761a626e5"
)

var (
	//go:embed schemas/mcpb-manifest-v0.4.schema.json
	manifestSchemaJSON []byte
	//go:embed schemas/server-2025-12-11.schema.json
	serverSchemaJSON []byte

	manifestSchemaOnce sync.Once
	manifestSchema     *jsonschema.Schema
	manifestSchemaErr  error
	serverSchemaOnce   sync.Once
	serverSchema       *jsonschema.Schema
	serverSchemaErr    error
)

// ValidateManifestSchema validates raw manifest JSON against the pinned MCPB
// v0.4 schema embedded in this package. It never follows a network reference.
func ValidateManifestSchema(raw []byte) error {
	schema, err := compiledManifestSchema()
	if err != nil {
		return err
	}
	return validateRawSchema(raw, schema, "MCPB manifest")
}

// ValidateServerJSON validates raw Registry metadata against the pinned
// 2025-12-11 official Registry schema embedded in this package. Callers should
// apply release-specific semantic checks after this structural validation.
func ValidateServerJSON(raw []byte) error {
	schema, err := compiledServerSchema()
	if err != nil {
		return err
	}
	return validateRawSchema(raw, schema, "server.json")
}

func compiledManifestSchema() (*jsonschema.Schema, error) {
	manifestSchemaOnce.Do(func() {
		manifestSchema, manifestSchemaErr = compileSchema(manifestSchemaResource, manifestSchemaSHA256, manifestSchemaJSON)
	})
	if manifestSchemaErr != nil {
		return nil, fmt.Errorf("compile embedded MCPB manifest schema: %w", manifestSchemaErr)
	}
	return manifestSchema, nil
}

func compiledServerSchema() (*jsonschema.Schema, error) {
	serverSchemaOnce.Do(func() {
		serverSchema, serverSchemaErr = compileSchema(serverSchemaResource, serverSchemaSHA256, serverSchemaJSON)
	})
	if serverSchemaErr != nil {
		return nil, fmt.Errorf("compile embedded server.json schema: %w", serverSchemaErr)
	}
	return serverSchema, nil
}

func compileSchema(resource, expectedSHA string, raw []byte) (*jsonschema.Schema, error) {
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	if digest != expectedSHA {
		return nil, fmt.Errorf("vendored schema SHA-256 = %s, want %s", digest, expectedSHA)
	}
	var document any
	if err := jsonv2.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("decode vendored schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft7)
	compiler.AssertFormat()
	if err := compiler.AddResource(resource, document); err != nil {
		return nil, err
	}
	return compiler.Compile(resource)
}

func validateRawSchema(raw []byte, schema *jsonschema.Schema, label string) error {
	var value any
	if err := jsonv2.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("decode %s: %w", label, err)
	}
	if value == nil {
		return fmt.Errorf("%s must be a JSON object", label)
	}
	if err := schema.Validate(value); err != nil {
		return fmt.Errorf("validate %s against vendored schema: %w", label, err)
	}
	return nil
}
