package sqlquery_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/ozgurcd/gograph/internal/sqlquery"
)

func TestClassifyPostgreSQLStaticStatements(t *testing.T) {
	tests := []struct {
		name   string
		query  string
		verb   string
		access string
		status string
		tables []sqlquery.TableRef
	}{
		{
			name:  "select joins and comma sources",
			query: `SELECT u.id FROM public.users AS u JOIN "Audit"."Events" e ON e.user_id = u.id, organisations o WHERE u.active`,
			verb:  "SELECT", access: "read", status: "exact",
			tables: []sqlquery.TableRef{{Name: "public.users", Access: "read"}, {Name: "organisations", Access: "read"}, {Name: `"Audit"."Events"`, Access: "read"}},
		},
		{
			name:  "insert select",
			query: `INSERT INTO archive.users (id) SELECT id FROM public.users ON CONFLICT (id) DO NOTHING RETURNING id`,
			verb:  "INSERT", access: "write", status: "exact",
			tables: []sqlquery.TableRef{{Name: "archive.users", Access: "write"}, {Name: "public.users", Access: "read"}},
		},
		{
			name:  "update from",
			query: `UPDATE ONLY public.users u SET active = false FROM suspensions s WHERE s.user_id = u.id`,
			verb:  "UPDATE", access: "write", status: "exact",
			tables: []sqlquery.TableRef{{Name: "public.users", Access: "write"}, {Name: "suspensions", Access: "read"}},
		},
		{
			name:  "delete using",
			query: `DELETE FROM sessions s USING expired_users e WHERE e.id = s.user_id`,
			verb:  "DELETE", access: "write", status: "exact",
			tables: []sqlquery.TableRef{{Name: "sessions", Access: "write"}, {Name: "expired_users", Access: "read"}},
		},
		{
			name:  "cte resolves actual verb and excludes cte identity",
			query: `WITH recent AS (SELECT id FROM audit.events), nested AS (SELECT id FROM recent) UPDATE users SET seen = true FROM nested WHERE users.id = nested.id`,
			verb:  "UPDATE", access: "write", status: "exact",
			tables: []sqlquery.TableRef{{Name: "users", Access: "write"}, {Name: "audit.events", Access: "read"}},
		},
		{
			name:  "data modifying cte elevates statement access",
			query: `WITH changed AS (UPDATE public.users SET active = false RETURNING id) SELECT id FROM changed`,
			verb:  "SELECT", access: "write", status: "exact",
			tables: []sqlquery.TableRef{{Name: "public.users", Access: "write"}},
		},
		{
			name:  "merge",
			query: `MERGE INTO inventory i USING staged_inventory s ON i.id = s.id WHEN MATCHED THEN UPDATE SET qty = s.qty`,
			verb:  "MERGE", access: "write", status: "exact",
			tables: []sqlquery.TableRef{{Name: "inventory", Access: "write"}, {Name: "staged_inventory", Access: "read"}},
		},
		{
			name:  "create index",
			query: `CREATE UNIQUE INDEX users_email_idx ON public.users (email)`,
			verb:  "CREATE", access: "ddl", status: "exact",
			tables: []sqlquery.TableRef{{Name: "public.users", Access: "ddl"}},
		},
		{
			name:  "drop list",
			query: `DROP TABLE IF EXISTS public.old_users, audit.old_events CASCADE`,
			verb:  "DROP", access: "ddl", status: "exact",
			tables: []sqlquery.TableRef{{Name: "public.old_users", Access: "ddl"}, {Name: "audit.old_events", Access: "ddl"}},
		},
		{
			name:  "create materialized view from source",
			query: `CREATE MATERIALIZED VIEW reporting.active_users AS SELECT id FROM public.users`,
			verb:  "CREATE", access: "ddl", status: "exact",
			tables: []sqlquery.TableRef{{Name: "reporting.active_users", Access: "ddl"}, {Name: "public.users", Access: "read"}},
		},
		{
			name:  "alter table modifiers",
			query: `ALTER TABLE IF EXISTS ONLY public.users ADD COLUMN active boolean`,
			verb:  "ALTER", access: "ddl", status: "exact",
			tables: []sqlquery.TableRef{{Name: "public.users", Access: "ddl"}},
		},
		{
			name:  "truncate list",
			query: `TRUNCATE TABLE public.sessions, audit.events RESTART IDENTITY`,
			verb:  "TRUNCATE", access: "ddl", status: "exact",
			tables: []sqlquery.TableRef{{Name: "public.sessions", Access: "ddl"}, {Name: "audit.events", Access: "ddl"}},
		},
		{
			name:  "comments and dollar quoted bodies are ignored",
			query: `/* FROM fake */ SELECT $$FROM ignored$$ AS body FROM real_table -- JOIN fake`,
			verb:  "SELECT", access: "read", status: "exact",
			tables: []sqlquery.TableRef{{Name: "real_table", Access: "read"}},
		},
		{
			name:  "select without table",
			query: `SELECT now(), 'FROM ignored'`,
			verb:  "SELECT", access: "read", status: "exact", tables: []sqlquery.TableRef{},
		},
		{
			name:  "partial malformed static statement",
			query: `UPDATE`, verb: "UPDATE", access: "write", status: "partial", tables: []sqlquery.TableRef{},
		},
		{
			name:  "multiple statements remain visible but are not labeled exact",
			query: `SELECT id FROM users; DELETE FROM audit_log`, verb: "SELECT", access: "read", status: "partial",
			tables: []sqlquery.TableRef{{Name: "users", Access: "read"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sqlquery.ClassifyPostgreSQL(tt.query)
			if got.Verb != tt.verb || got.Access != tt.access || got.Status != tt.status || !reflect.DeepEqual(got.Tables, tt.tables) {
				t.Fatalf("ClassifyPostgreSQL() = %+v, want verb=%s access=%s status=%s tables=%+v", got, tt.verb, tt.access, tt.status, tt.tables)
			}
		})
	}
}

func TestPostgreSQLFilterNormalizationAndTableMatching(t *testing.T) {
	for input, want := range map[string]string{
		"Users":            "users",
		"public.Users":     "public.users",
		`"Tenant"."Users"`: `"Tenant"."Users"`,
	} {
		got, err := sqlquery.NormalizeTableSelector(input)
		if err != nil || got != want {
			t.Errorf("NormalizeTableSelector(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if !sqlquery.TableMatches("public.users", "users") || !sqlquery.TableMatches("public.users", "public.users") {
		t.Fatal("unqualified and qualified selectors must match public.users")
	}
	if sqlquery.TableMatches(`"Tenant"."Users"`, "users") {
		t.Fatal("unquoted users must not match case-sensitive quoted Users")
	}
	if !sqlquery.TableMatches(`"Tenant"."Users"`, `"Tenant"."Users"`) {
		t.Fatal("exact quoted selector must match")
	}
	if _, err := sqlquery.NormalizeVerb("UPSERT"); err == nil {
		t.Fatal("UPSERT must not be accepted as a PostgreSQL verb; ON CONFLICT is INSERT")
	}
	if got, err := sqlquery.NormalizeVerb("select"); err != nil || got != "SELECT" {
		t.Fatalf("NormalizeVerb(select) = %q, %v", got, err)
	}
	if got, err := sqlquery.NormalizeAccess("WRITE"); err != nil || got != "write" {
		t.Fatalf("NormalizeAccess(WRITE) = %q, %v", got, err)
	}
}

func FuzzClassifyPostgreSQL(f *testing.F) {
	for _, seed := range []string{
		"SELECT id FROM public.users",
		"WITH changed AS (UPDATE users SET active = false RETURNING id) SELECT * FROM changed",
		"/* unterminated",
		"SELECT $$unterminated",
		"UPDATE \"unterminated",
		string([]byte{0xff, 0xfe, 'S', 'E', 'L', 'E', 'C', 'T'}),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, query string) {
		result := sqlquery.ClassifyPostgreSQL(query)
		if result.Verb == "" {
			if result.Access != "" || result.Status != sqlquery.StatusUnknown || len(result.Tables) != 0 {
				t.Fatalf("unknown classification has inconsistent metadata: %+v", result)
			}
			return
		}
		if _, err := sqlquery.NormalizeVerb(result.Verb); err != nil {
			t.Fatalf("classifier returned unsupported verb: %+v", result)
		}
		if _, err := sqlquery.NormalizeAccess(result.Access); err != nil {
			t.Fatalf("classifier returned unsupported access: %+v", result)
		}
		if result.Status != sqlquery.StatusExact && result.Status != sqlquery.StatusPartial {
			t.Fatalf("classifier returned inconsistent status: %+v", result)
		}
	})
}

func TestClassifyPostgreSQLBoundsNestedCTEs(t *testing.T) {
	query := "SELECT 1"
	for i := 0; i < 100; i++ {
		query = fmt.Sprintf("WITH c%d AS (%s) SELECT * FROM c%d", i, query, i)
	}
	got := sqlquery.ClassifyPostgreSQL(query)
	if got.Verb != "SELECT" || got.Status != sqlquery.StatusPartial {
		t.Fatalf("deep CTE classification must degrade safely: %+v", got)
	}
}
