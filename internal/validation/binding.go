package validation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	jsonv1 "encoding/json"
	jsonv2 "encoding/json/v2"
	"fmt"
	"go/token"
	"path"
	"strings"
	"unicode"
)

func ParseBinding(data []byte) (Binding, string, error) {
	rawSum := sha256.Sum256(data)
	rawFingerprint := hex.EncodeToString(rawSum[:])
	if len(bytes.TrimSpace(data)) == 0 {
		return Binding{}, rawFingerprint, fmt.Errorf("binding JSON is empty")
	}
	var binding Binding
	if err := jsonv2.Unmarshal(data, &binding, jsonv2.RejectUnknownMembers(true)); err != nil {
		return Binding{}, rawFingerprint, fmt.Errorf("decode binding: %w", err)
	}
	if err := validateBinding(binding); err != nil {
		return Binding{}, rawFingerprint, err
	}
	canonical, err := jsonv1.Marshal(binding)
	if err != nil {
		return Binding{}, rawFingerprint, fmt.Errorf("encode canonical binding: %w", err)
	}
	sum := sha256.Sum256(append(canonical, '\n'))
	return binding, hex.EncodeToString(sum[:]), nil
}

func validateBinding(binding Binding) error {
	if binding.SchemaVersion != BindingSchemaVersion {
		return fmt.Errorf("unsupported binding schema %q", binding.SchemaVersion)
	}
	if binding.RequiredPrecision != PrecisionAST && binding.RequiredPrecision != PrecisionPrecise {
		return fmt.Errorf("invalid required_precision %q", binding.RequiredPrecision)
	}

	switch binding.Predicate {
	case PredicateSymbolExists:
		if binding.Subject.Kind != ReferenceSymbol {
			return fmt.Errorf("symbol_exists subject must be a symbol")
		}
		if binding.Object != nil {
			return fmt.Errorf("symbol_exists must not contain object")
		}
	case PredicatePackageImports:
		if binding.Subject.Kind != ReferencePackage || binding.Object == nil || binding.Object.Kind != ReferencePackage {
			return fmt.Errorf("package_imports requires package subject and object")
		}
	case PredicateCallEdgeExists:
		if binding.Subject.Kind != ReferenceSymbol || binding.Object == nil || binding.Object.Kind != ReferenceSymbol {
			return fmt.Errorf("call_edge_exists requires symbol subject and object")
		}
	case PredicateTypeImplements:
		if binding.Subject.Kind != ReferenceSymbol || binding.Object == nil || binding.Object.Kind != ReferenceSymbol {
			return fmt.Errorf("type_implements requires symbol subject and object")
		}
	default:
		return fmt.Errorf("unsupported predicate %q", binding.Predicate)
	}
	if err := validateReference(binding.Subject); err != nil {
		return fmt.Errorf("invalid subject: %w", err)
	}
	if binding.Object != nil {
		if err := validateReference(*binding.Object); err != nil {
			return fmt.Errorf("invalid object: %w", err)
		}
	}
	return nil
}

func validateReference(reference Reference) error {
	if reference.Language != "go" {
		return fmt.Errorf("unsupported language %q", reference.Language)
	}
	switch reference.Kind {
	case ReferencePackage:
		if !validImportPath(reference.ID) {
			return fmt.Errorf("malformed package import path %q", reference.ID)
		}
	case ReferenceSymbol:
		if strings.HasPrefix(reference.ID, "_/") {
			return fmt.Errorf("absolute-path-derived symbol identity is unstable")
		}
		if !validSymbolID(reference.ID) {
			return fmt.Errorf("malformed module-qualified symbol ID %q", reference.ID)
		}
	default:
		return fmt.Errorf("invalid reference kind %q", reference.Kind)
	}
	return nil
}

func validSymbolID(id string) bool {
	packagePath, declaration, ok := strings.Cut(id, "::")
	if !ok || strings.Contains(declaration, "::") || !validImportPath(packagePath) {
		return false
	}
	if token.IsIdentifier(declaration) {
		return true
	}
	close := strings.Index(declaration, ").")
	if !strings.HasPrefix(declaration, "(") || close < 2 {
		return false
	}
	receiver := declaration[1:close]
	receiver = strings.TrimPrefix(receiver, "*")
	return token.IsIdentifier(receiver) && token.IsIdentifier(declaration[close+2:])
}

func validImportPath(importPath string) bool {
	if importPath == "" || strings.HasPrefix(importPath, "/") || strings.HasPrefix(importPath, "_") || path.Clean(importPath) != importPath {
		return false
	}
	for part := range strings.SplitSeq(importPath, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	for _, r := range importPath {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune(".-_~", r) || r == '/' {
			continue
		}
		return false
	}
	return true
}
