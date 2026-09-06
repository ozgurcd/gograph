package workspace

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/ozgurcd/gograph/internal/sourcefs"
	"gopkg.in/yaml.v3"
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

func FindRoot(start string) (string, error) {
	if start == "" {
		start = "."
	}
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve workspace start: %w", err)
	}
	info, statErr := os.Stat(abs)
	if statErr == nil && !info.IsDir() {
		abs = filepath.Dir(abs)
	}
	for {
		manifest := filepath.Join(abs, ManifestFile)
		entry, err := os.Lstat(manifest)
		if err == nil {
			if entry.Mode()&os.ModeSymlink != 0 || !entry.Mode().IsRegular() {
				return "", fmt.Errorf("unsafe workspace manifest %s: must be a regular non-linked file", manifest)
			}
			return abs, nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect workspace manifest %s: %w", manifest, err)
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", fmt.Errorf("no %s found from %s; create a regular manifest beneath the workspace root (run 'gograph workspace --help' for an example)", ManifestFile, start)
		}
		abs = parent
	}
}

func LoadManifest(root string) (Manifest, string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return Manifest{}, "", err
	}
	reader, err := sourcefs.Open(root)
	if err != nil {
		return Manifest{}, "", fmt.Errorf("open workspace root: %w", err)
	}
	defer func() { _ = reader.Close() }()
	data, err := reader.ReadRegularFile(ManifestFile)
	if err != nil {
		return Manifest{}, "", fmt.Errorf("read workspace manifest: %w", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, "", fmt.Errorf("parse workspace manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Manifest{}, "", fmt.Errorf("parse workspace manifest: multiple YAML documents are not allowed")
		}
		return Manifest{}, "", fmt.Errorf("parse workspace manifest trailing document: %w", err)
	}
	if err := normalizeAndValidateManifest(root, &manifest); err != nil {
		return Manifest{}, "", err
	}
	canonical, err := json.Marshal(manifest)
	if err != nil {
		return Manifest{}, "", fmt.Errorf("canonicalize workspace manifest: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return manifest, hex.EncodeToString(sum[:]), nil
}

func normalizeAndValidateManifest(root string, manifest *Manifest) error {
	if manifest.SchemaVersion != ManifestSchemaVersion {
		return fmt.Errorf("unsupported workspace manifest schema %q", manifest.SchemaVersion)
	}
	manifest.Name = strings.TrimSpace(manifest.Name)
	if err := validateDisplayText("workspace name", manifest.Name, 128); err != nil {
		return err
	}
	if len(manifest.Repositories) == 0 {
		return fmt.Errorf("workspace must contain at least one repository")
	}
	if manifest.Defaults.Precision == "" {
		manifest.Defaults.Precision = "ast"
	}
	if manifest.Defaults.Precision != "ast" && manifest.Defaults.Precision != "precise" {
		return fmt.Errorf("workspace defaults.precision must be ast or precise")
	}
	rootReader, err := sourcefs.Open(root)
	if err != nil {
		return fmt.Errorf("open workspace confinement root: %w", err)
	}
	defer func() { _ = rootReader.Close() }()
	repositoryIDs := make(map[string]bool)
	canonicalPaths := make(map[string]string)
	for index := range manifest.Repositories {
		repo := &manifest.Repositories[index]
		if !identifierPattern.MatchString(repo.ID) {
			return fmt.Errorf("repositories[%d].id %q is invalid", index, repo.ID)
		}
		if repositoryIDs[repo.ID] {
			return fmt.Errorf("duplicate repository id %q", repo.ID)
		}
		repositoryIDs[repo.ID] = true
		if err := validateDisplayText(fmt.Sprintf("repository %q path", repo.ID), repo.Path, 4096); err != nil {
			return err
		}
		clean := filepath.Clean(repo.Path)
		if repo.Path == "" || clean == "." || !filepath.IsLocal(clean) {
			return fmt.Errorf("repository %q path must be a relative descendant of the workspace root", repo.ID)
		}
		if err := rootReader.ValidateDirectory(clean); err != nil {
			return fmt.Errorf("repository %q path violates workspace confinement: %w", repo.ID, err)
		}
		canonical, err := filepath.EvalSymlinks(filepath.Join(root, clean))
		if err != nil {
			return fmt.Errorf("resolve repository %q path: %w", repo.ID, err)
		}
		if other := canonicalPaths[canonical]; other != "" {
			return fmt.Errorf("repositories %q and %q use the same canonical path", other, repo.ID)
		}
		canonicalPaths[canonical] = repo.ID
		repo.Path = filepath.ToSlash(clean)
		if repo.Precision == "" {
			repo.Precision = manifest.Defaults.Precision
		}
		if repo.Precision != "ast" && repo.Precision != "precise" {
			return fmt.Errorf("repository %q precision must be ast or precise", repo.ID)
		}
		serviceIDs := make(map[string]bool)
		httpServiceCount := 0
		for serviceIndex := range repo.Services {
			service := &repo.Services[serviceIndex]
			if !identifierPattern.MatchString(service.ID) {
				return fmt.Errorf("repository %q service id %q is invalid", repo.ID, service.ID)
			}
			if serviceIDs[service.ID] {
				return fmt.Errorf("repository %q has duplicate service id %q", repo.ID, service.ID)
			}
			serviceIDs[service.ID] = true
			if len(service.HTTP.Authorities) > 0 {
				httpServiceCount++
			}
			authorities := make(map[string]bool)
			for authorityIndex, authority := range service.HTTP.Authorities {
				authority = strings.ToLower(strings.TrimSpace(authority))
				if authority == "" || len(authority) > 255 || containsControlOrSpace(authority) || strings.ContainsAny(authority, "/?#@\\") {
					return fmt.Errorf("repository %q service %q has invalid HTTP authority %q", repo.ID, service.ID, authority)
				}
				if authorities[authority] {
					return fmt.Errorf("repository %q service %q repeats normalized HTTP authority %q", repo.ID, service.ID, authority)
				}
				authorities[authority] = true
				service.HTTP.Authorities[authorityIndex] = authority
			}
			sort.Strings(service.HTTP.Authorities)
		}
		if httpServiceCount > 1 {
			return fmt.Errorf("repository %q configures multiple HTTP services; workspace.v1 requires one HTTP service per repository", repo.ID)
		}
		sort.Slice(repo.Services, func(i, j int) bool { return repo.Services[i].ID < repo.Services[j].ID })
		if err := validateHTTPClients(repo); err != nil {
			return err
		}
	}
	sort.Slice(manifest.Repositories, func(i, j int) bool { return manifest.Repositories[i].ID < manifest.Repositories[j].ID })
	if len(manifest.Scopes) == 0 {
		ids := make([]string, 0, len(manifest.Repositories))
		for _, repo := range manifest.Repositories {
			ids = append(ids, repo.ID)
		}
		manifest.Scopes = []ScopeConfig{{ID: "default", Repositories: ids}}
		if manifest.DefaultScope == "" {
			manifest.DefaultScope = "default"
		}
	}
	scopeIDs := make(map[string]bool)
	for index := range manifest.Scopes {
		scope := &manifest.Scopes[index]
		if !identifierPattern.MatchString(scope.ID) {
			return fmt.Errorf("scopes[%d].id %q is invalid", index, scope.ID)
		}
		if scopeIDs[scope.ID] {
			return fmt.Errorf("duplicate scope id %q", scope.ID)
		}
		scopeIDs[scope.ID] = true
		if len(scope.Repositories) == 0 {
			return fmt.Errorf("scope %q has no repositories", scope.ID)
		}
		seen := make(map[string]bool)
		for _, id := range scope.Repositories {
			if !repositoryIDs[id] {
				return fmt.Errorf("scope %q references unknown repository %q", scope.ID, id)
			}
			if seen[id] {
				return fmt.Errorf("scope %q repeats repository %q", scope.ID, id)
			}
			seen[id] = true
		}
		sort.Strings(scope.Repositories)
	}
	sort.Slice(manifest.Scopes, func(i, j int) bool { return manifest.Scopes[i].ID < manifest.Scopes[j].ID })
	if manifest.DefaultScope != "" && !scopeIDs[manifest.DefaultScope] {
		return fmt.Errorf("default_scope %q does not name a configured scope", manifest.DefaultScope)
	}
	if err := validateScopedServiceOwnership(*manifest); err != nil {
		return err
	}
	return nil
}

// ResolveMemberRoot repeats the manifest's confinement checks immediately
// before a member is used. Workspace member paths are never accepted as
// arbitrary filesystem paths.
func ResolveMemberRoot(root string, config RepositoryConfig) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root links: %w", err)
	}
	clean := filepath.Clean(filepath.FromSlash(config.Path))
	if config.Path == "" || clean == "." || !filepath.IsLocal(clean) {
		return "", fmt.Errorf("repository %q path must be a relative descendant of the workspace root", config.ID)
	}
	reader, err := sourcefs.Open(realRoot)
	if err != nil {
		return "", fmt.Errorf("open workspace confinement root: %w", err)
	}
	defer func() { _ = reader.Close() }()
	if err := reader.ValidateDirectory(clean); err != nil {
		return "", fmt.Errorf("repository %q path violates workspace confinement: %w", config.ID, err)
	}
	memberRoot, err := filepath.EvalSymlinks(filepath.Join(realRoot, clean))
	if err != nil {
		return "", fmt.Errorf("resolve repository %q path: %w", config.ID, err)
	}
	rel, err := filepath.Rel(realRoot, memberRoot)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("repository %q path escapes workspace root", config.ID)
	}
	return memberRoot, nil
}

func validateDisplayText(field, value string, limit int) error {
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	if len(value) > limit {
		return fmt.Errorf("%s exceeds %d bytes", field, limit)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s contains control characters", field)
		}
	}
	return nil
}

func containsControlOrSpace(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return true
		}
	}
	return false
}

func validateScopedServiceOwnership(manifest Manifest) error {
	repositories := make(map[string]RepositoryConfig, len(manifest.Repositories))
	for _, repository := range manifest.Repositories {
		repositories[repository.ID] = repository
	}
	for _, scope := range manifest.Scopes {
		logicalOwners := make(map[string][]serviceOwner)
		aliasOwners := make(map[string][]serviceOwner)
		for _, repositoryID := range scope.Repositories {
			for _, service := range repositories[repositoryID].Services {
				if len(service.HTTP.Authorities) == 0 {
					continue
				}
				owner := serviceOwner{repositoryID: repositoryID, authorityID: service.ID, aliases: service.HTTP.Authorities, shared: service.HTTP.SharedAuthority}
				logicalOwners[service.ID] = append(logicalOwners[service.ID], owner)
				for _, alias := range service.HTTP.Authorities {
					aliasOwners[alias] = append(aliasOwners[alias], owner)
				}
			}
		}
		for authorityID, owners := range logicalOwners {
			if len(owners) > 1 && !allServiceOwnersShared(owners) {
				return fmt.Errorf("scope %q logical HTTP authority %q has multiple owners without explicit shared_authority", scope.ID, authorityID)
			}
		}
		for alias, owners := range aliasOwners {
			if len(owners) > 1 && !allServiceOwnersShared(owners) {
				return fmt.Errorf("scope %q HTTP authority alias %q has multiple owners without explicit shared_authority", scope.ID, alias)
			}
		}
	}
	return nil
}

func allServiceOwnersShared(owners []serviceOwner) bool {
	for _, owner := range owners {
		if !owner.shared {
			return false
		}
	}
	return true
}

func RepositoryByID(manifest Manifest, id string) (RepositoryConfig, bool) {
	index := sort.Search(len(manifest.Repositories), func(i int) bool { return manifest.Repositories[i].ID >= id })
	if index < len(manifest.Repositories) && manifest.Repositories[index].ID == id {
		return manifest.Repositories[index], true
	}
	return RepositoryConfig{}, false
}
