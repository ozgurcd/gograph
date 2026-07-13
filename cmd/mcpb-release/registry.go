package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type registryVersion struct {
	Server json.RawMessage `json:"server"`
	Meta   struct {
		Official struct {
			Status string `json:"status"`
		} `json:"io.modelcontextprotocol.registry/official"`
	} `json:"_meta"`
}

func registryState(ctx context.Context, endpoint string, local []byte, name, version string) (string, error) {
	exactURL := strings.TrimRight(endpoint, "/") + "/" + url.PathEscape(name) + "/versions/" + url.PathEscape(version)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, exactURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "gograph-mcpb-release/1")
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("query MCP Registry: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return "", fmt.Errorf("read MCP Registry response: %w", err)
	}
	if response.StatusCode == http.StatusNotFound {
		return "missing", nil
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("MCP Registry returned HTTP %d: %s", response.StatusCode, bodySnippet(body))
	}

	var published registryVersion
	if err := json.Unmarshal(body, &published); err != nil {
		return "", fmt.Errorf("decode MCP Registry response: %w", err)
	}
	if len(published.Server) == 0 || string(published.Server) == "null" {
		return "", fmt.Errorf("MCP Registry response is missing server metadata")
	}
	var identity struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(published.Server, &identity); err != nil {
		return "", fmt.Errorf("decode MCP Registry server identity: %w", err)
	}
	if identity.Name != name || identity.Version != version {
		return "", fmt.Errorf("MCP Registry exact lookup returned %s %s, want %s %s", identity.Name, identity.Version, name, version)
	}
	matches, err := localMetadataMatchesPublished(local, published.Server)
	if err != nil {
		return "", err
	}
	if !matches {
		return "", fmt.Errorf("MCP Registry already contains immutable %s %s with different metadata", name, version)
	}
	if published.Meta.Official.Status != "active" {
		return "pending", nil
	}
	return "matching", nil
}

func sortRegistryPackages(value map[string]any) {
	packages, ok := value["packages"].([]any)
	if !ok {
		return
	}
	sort.SliceStable(packages, func(left, right int) bool {
		leftPackage, leftOK := packages[left].(map[string]any)
		rightPackage, rightOK := packages[right].(map[string]any)
		if !leftOK || !rightOK {
			return false
		}
		return fmt.Sprint(leftPackage["identifier"]) < fmt.Sprint(rightPackage["identifier"])
	})
	value["packages"] = packages
}
