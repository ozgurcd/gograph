package workspace

import (
	"fmt"
	"go/token"
	"net/url"
	"sort"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
)

func validateHTTPClients(repo *RepositoryConfig) error {
	seen := make(map[string]bool)
	for _, client := range repo.HTTPClients {
		if !validHTTPBase(client.Base) {
			return fmt.Errorf("repository %q HTTP client base %q must be a Go identifier/selector or env:KEY", repo.ID, client.Base)
		}
		if seen[client.Base] {
			return fmt.Errorf("repository %q repeats HTTP client base %q", repo.ID, client.Base)
		}
		seen[client.Base] = true
		if !identifierPattern.MatchString(client.AuthorityID) {
			return fmt.Errorf("repository %q HTTP client %q has invalid authority_id %q", repo.ID, client.Base, client.AuthorityID)
		}
		if !validHTTPPrefix(client.PathPrefix) {
			return fmt.Errorf("repository %q HTTP client %q path_prefix must be an absolute URL path without authority, query, fragment, or dot segments", repo.ID, client.Base)
		}
	}
	sort.Slice(repo.HTTPClients, func(i, j int) bool { return repo.HTTPClients[i].Base < repo.HTTPClients[j].Base })
	return nil
}

func validHTTPBase(base string) bool {
	if base == "" || len(base) > 1024 || containsControlOrSpace(base) {
		return false
	}
	if key, ok := strings.CutPrefix(base, "env:"); ok {
		return key != "" && !strings.ContainsAny(key, "=\x00")
	}
	for _, part := range strings.Split(base, ".") {
		if part == "_" || !token.IsIdentifier(part) {
			return false
		}
	}
	return true
}

func validHTTPPrefix(prefix string) bool {
	if prefix == "" {
		return true
	}
	if len(prefix) > 4096 || containsControlOrSpace(prefix) || !strings.HasPrefix(prefix, "/") || strings.HasPrefix(prefix, "//") || strings.ContainsAny(prefix, "?#\\") {
		return false
	}
	u, err := url.Parse(prefix)
	if err != nil || u.RawPath != "" || u.Host != "" || u.Scheme != "" || containsControlOrSpace(u.Path) || strings.Contains(u.Path, "\\") {
		return false
	}
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

// resolveHTTPDestination only sees the selected scope's owners. A configured
// authority in another scope cannot turn a diagnostic into a resolved edge.
func resolveHTTPDestination(member LoadedMember, call graph.HTTPCallEdge, services map[string][]serviceOwner, aliases map[string][]serviceOwner) ([]serviceOwner, *url.URL, string) {
	if !call.HasDynamic {
		parsed, err := url.Parse(call.URL)
		if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, nil, "invalid_or_unsupported_url"
		}
		owners := aliases[strings.ToLower(parsed.Host)]
		if len(owners) == 0 {
			owners = aliases[strings.ToLower(parsed.Hostname())]
		}
		if len(owners) == 0 {
			return nil, nil, "unconfigured_host"
		}
		return owners, parsed, ""
	}
	if call.URLBase == "" || !call.URLSuffixStatic {
		return nil, nil, "dynamic_url_not_bounded"
	}
	var configured *HTTPClientConfig
	for i := range member.Config.HTTPClients {
		candidate := &member.Config.HTTPClients[i]
		if candidate.Base == call.URLBase {
			if configured != nil {
				return nil, nil, "ambiguous_base_mapping"
			}
			configured = candidate
		}
	}
	if configured == nil {
		return nil, nil, "unconfigured_base"
	}
	// The manifest describes the complete base path. Concatenation (not Join or
	// ResolveReference) mirrors Go's string expression; a suffix cannot replace
	// the configured authority with a protocol-relative or absolute URL.
	if call.URLSuffix != "" && !strings.HasPrefix(call.URLSuffix, "/") && !strings.HasPrefix(call.URLSuffix, "?") && !strings.HasPrefix(call.URLSuffix, "#") {
		return nil, nil, "unsupported_url_suffix"
	}
	path := configured.PathPrefix + call.URLSuffix
	parsed, err := url.Parse(path)
	if err != nil || parsed.Host != "" || parsed.Scheme != "" || parsed.Opaque != "" || strings.HasPrefix(path, "//") || strings.Contains(path, "\\") {
		return nil, nil, "unsupported_url_suffix"
	}
	var owners []serviceOwner
	for _, candidates := range services {
		for _, owner := range candidates {
			if owner.authorityID == configured.AuthorityID {
				owners = append(owners, owner)
			}
		}
	}
	if len(owners) == 0 {
		return nil, nil, "authority_not_in_scope"
	}
	return owners, parsed, ""
}

func sortHTTPUnresolved(rows []HTTPUnresolved) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		left := nodeSortKey(a.Source) + "\x00" + a.File
		right := nodeSortKey(b.Source) + "\x00" + b.File
		if left != right {
			return left < right
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Method+"\x00"+a.URL+"\x00"+a.Base+"\x00"+a.Reason < b.Method+"\x00"+b.URL+"\x00"+b.Base+"\x00"+b.Reason
	})
}
