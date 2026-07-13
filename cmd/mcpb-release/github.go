package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ozgurcd/gograph/internal/mcpbundle"
)

type githubRelease struct {
	TagName    string        `json:"tag_name"`
	Draft      bool          `json:"draft"`
	Prerelease bool          `json:"prerelease"`
	Assets     []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	Digest             string `json:"digest"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func githubReleaseState(
	ctx context.Context,
	repository string,
	tag string,
	serverBytes []byte,
	expectedMCPB map[string]string,
) (string, error) {
	apiBase := strings.TrimRight(os.Getenv("GITHUB_API_URL"), "/")
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	endpoint := fmt.Sprintf("%s/repos/%s/releases/tags/%s", apiBase, repository, url.PathEscape(tag))
	body, status, err := githubGet(ctx, endpoint, "application/vnd.github+json")
	if err != nil {
		return "", err
	}
	if status == http.StatusNotFound {
		return "missing", nil
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("GitHub release lookup returned HTTP %d: %s", status, bodySnippet(body))
	}

	var release githubRelease
	if err := json.Unmarshal(body, &release); err != nil {
		return "", fmt.Errorf("decode GitHub release: %w", err)
	}
	if release.TagName != tag || release.Draft || release.Prerelease {
		return "", fmt.Errorf("existing release is not the final release for %s (tag=%q draft=%v prerelease=%v)", tag, release.TagName, release.Draft, release.Prerelease)
	}

	assets := make(map[string]githubAsset, len(release.Assets))
	for _, asset := range release.Assets {
		if _, duplicate := assets[asset.Name]; duplicate {
			return "", fmt.Errorf("GitHub release contains duplicate asset %q", asset.Name)
		}
		assets[asset.Name] = asset
	}
	for _, name := range append(ordinaryReleaseAssets(), "checksums.txt", "server.json") {
		if _, ok := assets[name]; !ok {
			return "", fmt.Errorf("existing GitHub release is incomplete: missing %s", name)
		}
	}
	for name := range expectedMCPB {
		if _, ok := assets[name]; !ok {
			return "", fmt.Errorf("existing GitHub release is incomplete: missing %s", name)
		}
	}

	remoteServer, err := downloadGitHubAsset(ctx, assets["server.json"])
	if err != nil {
		return "", err
	}
	if !bytes.Equal(bytes.TrimSpace(serverBytes), bytes.TrimSpace(remoteServer)) {
		return "", fmt.Errorf("published server.json differs from the tagged source")
	}
	if err := verifyAssetDigest(assets["server.json"], remoteServer); err != nil {
		return "", err
	}

	checksumBytes, err := downloadGitHubAsset(ctx, assets["checksums.txt"])
	if err != nil {
		return "", err
	}
	if err := verifyAssetDigest(assets["checksums.txt"], checksumBytes); err != nil {
		return "", err
	}
	checksums, err := parseChecksums(checksumBytes)
	if err != nil {
		return "", err
	}

	expectedArtifacts := ordinaryReleaseAssets()
	expectedArtifacts = append(expectedArtifacts, sortedKeys(expectedMCPB)...)
	for _, name := range expectedArtifacts {
		asset := assets[name]
		checksum, ok := checksums[name]
		if !ok {
			return "", fmt.Errorf("checksums.txt is missing %s", name)
		}
		if asset.Digest != "sha256:"+checksum {
			return "", fmt.Errorf("GitHub digest for %s is %q, want sha256:%s", name, asset.Digest, checksum)
		}
		if expected, ok := expectedMCPB[name]; ok && checksum != expected {
			return "", fmt.Errorf("published MCPB checksum for %s is %s, want %s", name, checksum, expected)
		}
	}
	if err := verifyPublishedMCPBs(ctx, assets, expectedMCPB, strings.TrimPrefix(tag, "v")); err != nil {
		return "", err
	}
	return "matching", nil
}

func verifyPublishedMCPBs(ctx context.Context, assets map[string]githubAsset, expected map[string]string, version string) error {
	directory, err := os.MkdirTemp("", "gograph-published-mcpb-")
	if err != nil {
		return fmt.Errorf("create published MCPB verification directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(directory) }()
	for _, target := range mcpbundle.Targets {
		name := target.ArtifactName(version)
		asset, ok := assets[name]
		if !ok {
			return fmt.Errorf("published release is missing %s", name)
		}
		contents, err := downloadGitHubAsset(ctx, asset)
		if err != nil {
			return err
		}
		if err := verifyAssetDigest(asset, contents); err != nil {
			return err
		}
		if _, err := mcpbundle.VerifyBundle(contents, target, version, expected[name]); err != nil {
			return fmt.Errorf("verify downloaded %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(directory, name), contents, 0o644); err != nil {
			return fmt.Errorf("stage downloaded %s: %w", name, err)
		}
	}
	project := filepath.Join(directory, "project with spaces;no-shell")
	if err := os.MkdirAll(project, 0o755); err != nil {
		return fmt.Errorf("create published MCPB smoke project: %w", err)
	}
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module example.com/gograph-published-mcpb\n\ngo 1.26.5\n"), 0o644); err != nil {
		return fmt.Errorf("write published MCPB smoke go.mod: %w", err)
	}
	if err := os.WriteFile(filepath.Join(project, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		return fmt.Errorf("write published MCPB smoke source: %w", err)
	}
	smokeCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if _, err := mcpbundle.SmokeNative(smokeCtx, directory, project, version); err != nil {
		return fmt.Errorf("smoke-test downloaded native MCPB: %w", err)
	}
	return nil
}

func githubGet(ctx context.Context, endpoint, accept string) ([]byte, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Accept", accept)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("GET %s: %w", endpoint, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return nil, 0, fmt.Errorf("read %s: %w", endpoint, err)
	}
	return body, response.StatusCode, nil
}

func downloadGitHubAsset(ctx context.Context, asset githubAsset) ([]byte, error) {
	if asset.BrowserDownloadURL == "" {
		return nil, fmt.Errorf("GitHub asset %q has no download URL", asset.Name)
	}
	body, status, err := githubGet(ctx, asset.BrowserDownloadURL, "application/octet-stream")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("download %s returned HTTP %d: %s", asset.Name, status, bodySnippet(body))
	}
	return body, nil
}

func verifyAssetDigest(asset githubAsset, contents []byte) error {
	digest := sha256.Sum256(contents)
	actual := "sha256:" + hex.EncodeToString(digest[:])
	if asset.Digest != actual {
		return fmt.Errorf("GitHub digest for %s is %q, computed %q", asset.Name, asset.Digest, actual)
	}
	return nil
}

func parseChecksums(contents []byte) (map[string]string, error) {
	result := make(map[string]string)
	for lineNumber, line := range strings.Split(string(contents), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 || len(fields[0]) != 64 {
			return nil, fmt.Errorf("invalid checksums.txt line %d", lineNumber+1)
		}
		if _, duplicate := result[fields[1]]; duplicate {
			return nil, fmt.Errorf("duplicate checksum for %s", fields[1])
		}
		result[fields[1]] = strings.ToLower(fields[0])
	}
	return result, nil
}

func ordinaryReleaseAssets() []string {
	return []string{
		"gograph_Darwin_arm64.tar.gz",
		"gograph_Darwin_x86_64.tar.gz",
		"gograph_Linux_arm64.tar.gz",
		"gograph_Linux_x86_64.tar.gz",
		"gograph_Windows_arm64.zip",
		"gograph_Windows_x86_64.zip",
	}
}

func bodySnippet(body []byte) string {
	const limit = 512
	if len(body) > limit {
		body = body[:limit]
	}
	return strings.TrimSpace(string(body))
}
