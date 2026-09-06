package search

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

// paginationBinding covers graph content and normalized selection. Page size
// is deliberately not part of selection identity.
func selectionBinding(fingerprint, schema string, selection any) (string, error) {
	h := sha256.New()
	encoder := json.NewEncoder(h)
	for _, value := range []any{schema, fingerprint, selection} {
		if err := encoder.Encode(value); err != nil {
			return "", fmt.Errorf("fingerprint pagination snapshot: %w", err)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func ResultSnapshotFingerprint(g *graph.Graph) (string, error) {
	// Encode one collection member at a time. Encoding Graph in one call would
	// make encoding/json allocate a graph-sized intermediate buffer even when
	// its writer is a digest. Include all exported fields automatically except
	// the untrusted, loader-supplied root. Nil and empty lists are equivalent.
	if g == nil {
		g = &graph.Graph{}
	}
	h := sha256.New()
	encoder := json.NewEncoder(h)
	if err := encoder.Encode("gograph.query-snapshot.v1"); err != nil {
		return "", err
	}
	value := reflect.ValueOf(*g)
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		if field.PkgPath != "" || field.Name == "Root" || field.Tag.Get("json") == "-" {
			continue
		}
		if err := encoder.Encode(field.Name); err != nil {
			return "", err
		}
		data := value.Field(i)
		if data.Kind() == reflect.Slice {
			if err := encoder.Encode(data.Len()); err != nil {
				return "", err
			}
			for j := 0; j < data.Len(); j++ {
				if err := encoder.Encode(data.Index(j).Interface()); err != nil {
					return "", err
				}
			}
		} else if err := encoder.Encode(data.Interface()); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func boundCursor(binding, offset string) string {
	return "v1." + binding + "." + offset
}

func cursorOffset(cursor, binding, kind string) (string, error) {
	if cursor == "" {
		return "", nil
	}
	parts := strings.Split(cursor, ".")
	if len(parts) != 3 || parts[0] != "v1" || len(parts[1]) != 64 || parts[2] == "" {
		return "", fmt.Errorf("invalid %s cursor; restart without cursor", kind)
	}
	if parts[1] != binding {
		return "", fmt.Errorf("%s cursor snapshot or filters changed; restart without cursor", kind)
	}
	return parts[2], nil
}
