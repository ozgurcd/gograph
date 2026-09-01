package parser_test

import (
	"go/token"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/parser"
)

func featuresDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata", "features")
}

func TestParseFile_Features(t *testing.T) {
	fset := token.NewFileSet()
	fixturePath := filepath.Join(featuresDir(), "features.go")
	result, err := parser.ParseFile(fset, fixturePath, "features/features.go", "example.com/features")
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	byName := make(map[string]graph.SymbolNode)
	for _, s := range result.Symbols {
		byName[s.Name] = s
	}

	t.Run("Struct Embeds Extracted", func(t *testing.T) {
		adminUser, ok := byName["AdminUser"]
		if !ok {
			t.Fatal("expected AdminUser symbol")
		}
		if len(adminUser.EmbeddedStructs) == 0 {
			t.Fatal("expected AdminUser to have embedded structs")
		}
		if adminUser.EmbeddedStructs[0] != "BaseUser" {
			t.Errorf("expected embedded struct 'BaseUser', got %q", adminUser.EmbeddedStructs[0])
		}
	})

	t.Run("Errors and Panics Extracted", func(t *testing.T) {
		var foundPanic, foundErrorf, foundNew bool
		for _, call := range result.Calls {
			if call.CalleeRaw == "panic" {
				foundPanic = true
			}
			if call.CalleeRaw == "fmt.Errorf" {
				foundErrorf = true
			}
			if call.CalleeRaw == "errors.New" {
				foundNew = true
			}
		}

		if !foundPanic {
			t.Error("expected to find panic() call")
		}
		if !foundErrorf {
			t.Error("expected to find fmt.Errorf() call")
		}
		if !foundNew {
			t.Error("expected to find errors.New() call")
		}
	})

	t.Run("SQL Queries Extracted", func(t *testing.T) {
		foundQuery := false
		for _, call := range result.Calls {
			if call.CalleeRaw == "db.QueryRow" {
				foundQuery = true
			}
		}
		if !foundQuery {
			t.Error("expected to find SQL execution call via QueryRow")
		}
		if len(result.SQLs) != 1 {
			t.Fatalf("expected one classified SQL query, got %+v", result.SQLs)
		}
		sql := result.SQLs[0]
		if sql.Verb != "SELECT" || sql.Access != "read" || sql.Classification != "exact" {
			t.Fatalf("SQL classification = %+v", sql)
		}
		if len(sql.Tables) != 1 || sql.Tables[0].Name != "admins" || sql.Tables[0].Access != "read" {
			t.Fatalf("SQL tables = %+v", sql.Tables)
		}
	})

	t.Run("Return Usage Classified", func(t *testing.T) {
		usage := make(map[string]string) // callee → usage
		for _, c := range result.Calls {
			if c.CalleeRaw == "helper" && c.ReturnUsage != "" {
				usage[c.ReturnUsage] = c.ReturnUsage
			}
		}
		if usage["discarded"] == "" {
			t.Error("expected a 'discarded' call to helper()")
		}
		if usage["assigned"] == "" {
			t.Error("expected an 'assigned' call to helper()")
		}
		if usage["partially_ignored"] == "" {
			t.Error("expected a 'partially_ignored' call to helper()")
		}
	})

	t.Run("Struct Literals Extracted", func(t *testing.T) {
		byType := make(map[string][]graph.LiteralEdge)
		for _, lit := range result.Literals {
			byType[lit.TypeName] = append(byType[lit.TypeName], lit)
		}
		if len(byType["AdminUser"]) == 0 {
			t.Error("expected AdminUser literal site from MakeAdmin")
		}
		if len(byType["BaseUser"]) == 0 {
			t.Error("expected BaseUser literal site from MakeAdmin")
		}
		// Verify enclosing function is recorded
		for _, lit := range byType["AdminUser"] {
			if lit.Function == "" {
				t.Error("expected non-empty Function for AdminUser literal")
			}
			if lit.File == "" {
				t.Error("expected non-empty File for AdminUser literal")
			}
			if lit.Line == 0 {
				t.Error("expected non-zero Line for AdminUser literal")
			}
		}
	})
}

func TestParseSourceClassifiesEscapedPostgreSQLLiteral(t *testing.T) {
	source := []byte("package example\nfunc load(db interface{ Query(string) }) {\n\tdb.Query(\"SELECT id\\nFROM public.users\")\n}\n")
	result, err := parser.ParseSource(token.NewFileSet(), "query.go", source, "query.go", "example.com/query")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SQLs) != 1 {
		t.Fatalf("SQLs = %+v", result.SQLs)
	}
	query := result.SQLs[0]
	if query.Query != "SELECT id\nFROM public.users" || query.Verb != "SELECT" || len(query.Tables) != 1 || query.Tables[0].Name != "public.users" {
		t.Fatalf("escaped SQL literal classification = %+v", query)
	}
}
