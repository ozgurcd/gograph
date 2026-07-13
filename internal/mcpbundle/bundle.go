package mcpbundle

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"debug/buildinfo"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"strings"
	"time"
)

const (
	maxBundleSize   = 256 << 20
	maxManifestSize = 1 << 20
	maxLicenseSize  = 1 << 20
	maxBinarySize   = 192 << 20
	mainPackagePath = "github.com/ozgurcd/gograph/cmd/gograph"
)

var (
	zipEpoch   = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)
	shaPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// Verification describes one fully validated bundle.
type Verification struct {
	Target       Target
	Manifest     Manifest
	SHA256       string
	BinarySHA256 string
	Size         int64
}

type verifiedBundle struct {
	verification Verification
	binary       []byte
	license      []byte
}

// BuildBundle validates its inputs and returns a deterministic MCPB ZIP and
// the lowercase SHA-256 digest of the exact bytes returned.
func BuildBundle(manifest Manifest, binary, license []byte) ([]byte, string, error) {
	target, err := targetForBinary(binary)
	if err != nil {
		return nil, "", err
	}
	if err := ValidateManifest(manifest, manifest.Version, target); err != nil {
		return nil, "", err
	}
	if err := ValidateBinary(binary, target, manifest.Version); err != nil {
		return nil, "", err
	}
	if err := validateLicense(license); err != nil {
		return nil, "", err
	}
	manifestJSON, err := MarshalManifest(manifest, manifest.Version, target)
	if err != nil {
		return nil, "", err
	}

	var output bytes.Buffer
	zw := zip.NewWriter(&output)
	entries := []struct {
		name string
		mode fs.FileMode
		data []byte
	}{
		{name: "manifest.json", mode: 0o644, data: manifestJSON},
		{name: "LICENSE", mode: 0o644, data: license},
		{name: target.ServerPath(), mode: 0o755, data: binary},
	}
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate, Modified: zipEpoch}
		header.SetMode(entry.mode)
		writer, err := zw.CreateHeader(header)
		if err != nil {
			_ = zw.Close()
			return nil, "", fmt.Errorf("create MCPB entry %s: %w", entry.name, err)
		}
		if _, err := writer.Write(entry.data); err != nil {
			_ = zw.Close()
			return nil, "", fmt.Errorf("write MCPB entry %s: %w", entry.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, "", fmt.Errorf("close MCPB ZIP: %w", err)
	}
	if output.Len() > maxBundleSize {
		return nil, "", fmt.Errorf("MCPB size %d exceeds limit %d", output.Len(), maxBundleSize)
	}
	bundle := output.Bytes()
	digest := sha256.Sum256(bundle)
	return bundle, hex.EncodeToString(digest[:]), nil
}

// VerifyBundle validates the digest, exact ZIP layout, manifest, license, and
// embedded Go executable for one target. expectedSHA may be empty when callers
// only need the calculated digest.
func VerifyBundle(bundle []byte, target Target, version, expectedSHA string) (*Verification, error) {
	verified, err := verifyBundle(bundle, target, version, expectedSHA)
	if err != nil {
		return nil, err
	}
	result := verified.verification
	return &result, nil
}

func verifyBundle(bundle []byte, target Target, version, expectedSHA string) (*verifiedBundle, error) {
	if err := target.Validate(); err != nil {
		return nil, err
	}
	if err := ValidateVersion(version); err != nil {
		return nil, err
	}
	if len(bundle) == 0 || len(bundle) > maxBundleSize {
		return nil, fmt.Errorf("MCPB size %d is outside allowed range", len(bundle))
	}
	digest := sha256.Sum256(bundle)
	actualSHA := hex.EncodeToString(digest[:])
	if expectedSHA != "" {
		if !shaPattern.MatchString(expectedSHA) {
			return nil, fmt.Errorf("expected SHA-256 must be 64 lowercase hexadecimal characters")
		}
		if subtle.ConstantTimeCompare([]byte(actualSHA), []byte(expectedSHA)) != 1 {
			return nil, fmt.Errorf("MCPB SHA-256 = %s, want %s", actualSHA, expectedSHA)
		}
	}

	zr, err := zip.NewReader(bytes.NewReader(bundle), int64(len(bundle)))
	if err != nil {
		return nil, fmt.Errorf("open MCPB ZIP: %w", err)
	}
	wantNames := map[string]int64{
		"manifest.json":     maxManifestSize,
		"LICENSE":           maxLicenseSize,
		target.ServerPath(): maxBinarySize,
	}
	files := make(map[string]*zip.File, len(wantNames))
	for _, file := range zr.File {
		if !safeArchiveName(file.Name) {
			return nil, fmt.Errorf("unsafe MCPB entry name %q", file.Name)
		}
		limit, wanted := wantNames[file.Name]
		if !wanted {
			return nil, fmt.Errorf("unexpected MCPB entry %q", file.Name)
		}
		if _, duplicate := files[file.Name]; duplicate {
			return nil, fmt.Errorf("duplicate MCPB entry %q", file.Name)
		}
		if file.FileInfo().IsDir() || !file.Mode().IsRegular() {
			return nil, fmt.Errorf("MCPB entry %q is not a regular file", file.Name)
		}
		if file.Method != zip.Deflate {
			return nil, fmt.Errorf("MCPB entry %q is not deterministically deflated", file.Name)
		}
		if !file.Modified.Equal(zipEpoch) {
			return nil, fmt.Errorf("MCPB entry %q has non-deterministic timestamp %s", file.Name, file.Modified)
		}
		if file.UncompressedSize64 > uint64(limit) {
			return nil, fmt.Errorf("MCPB entry %q exceeds size limit", file.Name)
		}
		files[file.Name] = file
	}
	if len(files) != len(wantNames) {
		for name := range wantNames {
			if files[name] == nil {
				return nil, fmt.Errorf("MCPB is missing required entry %q", name)
			}
		}
	}
	if files["manifest.json"].Mode().Perm() != 0o644 || files["LICENSE"].Mode().Perm() != 0o644 {
		return nil, fmt.Errorf("manifest.json and LICENSE must use mode 0644")
	}
	if files[target.ServerPath()].Mode().Perm() != 0o755 {
		return nil, fmt.Errorf("bundled executable must use mode 0755")
	}

	manifestData, err := readZIPEntry(files["manifest.json"], maxManifestSize)
	if err != nil {
		return nil, err
	}
	manifest, err := DecodeManifest(manifestData, version, target)
	if err != nil {
		return nil, err
	}
	license, err := readZIPEntry(files["LICENSE"], maxLicenseSize)
	if err != nil {
		return nil, err
	}
	if err := validateLicense(license); err != nil {
		return nil, err
	}
	binary, err := readZIPEntry(files[target.ServerPath()], maxBinarySize)
	if err != nil {
		return nil, err
	}
	if err := ValidateBinary(binary, target, version); err != nil {
		return nil, err
	}
	binaryDigest := sha256.Sum256(binary)
	return &verifiedBundle{
		verification: Verification{
			Target:       target,
			Manifest:     manifest,
			SHA256:       actualSHA,
			BinarySHA256: hex.EncodeToString(binaryDigest[:]),
			Size:         int64(len(bundle)),
		},
		binary:  binary,
		license: license,
	}, nil
}

// ValidateBinary checks the Go executable's embedded build metadata. This is
// portable, so CI can validate all non-native release targets without running
// foreign executables.
func ValidateBinary(binary []byte, target Target, version string) error {
	if err := target.Validate(); err != nil {
		return err
	}
	if err := ValidateVersion(version); err != nil {
		return err
	}
	if len(binary) == 0 || len(binary) > maxBinarySize {
		return fmt.Errorf("binary size %d is outside allowed range", len(binary))
	}
	info, err := buildinfo.Read(bytes.NewReader(binary))
	if err != nil {
		return fmt.Errorf("read bundled Go build info: %w", err)
	}
	if info.Path != mainPackagePath {
		return fmt.Errorf("bundled main package = %q, want %q", info.Path, mainPackagePath)
	}
	settings := make(map[string]string, len(info.Settings))
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	wantSettings := map[string]string{
		"GOOS":        target.GOOS,
		"GOARCH":      target.GOARCH,
		"CGO_ENABLED": "0",
		"-buildmode":  "exe",
		"-compiler":   "gc",
		"-trimpath":   "true",
	}
	if target.GOARCH == "amd64" {
		wantSettings["GOAMD64"] = "v1"
	} else {
		wantSettings["GOARM64"] = "v8.0"
	}
	for key, want := range wantSettings {
		if got := settings[key]; got != want {
			return fmt.Errorf("bundled binary setting %s = %q, want %q", key, got, want)
		}
	}
	// Go 1.26 no longer consistently records -ldflags in BuildInfo. When it is
	// present, validate it exactly; always require the unique linked marker so a
	// dependency version cannot make a stale executable appear current.
	if ldflags := settings["-ldflags"]; ldflags != "" && !ldflagsSetVersion(ldflags, version) {
		return fmt.Errorf("bundled binary ldflags do not set main.version=%s", version)
	}
	if !bytes.Contains(binary, []byte("gograph-release-version=/"+version+"/")) {
		return fmt.Errorf("bundled binary does not contain exact linked release marker for %s", version)
	}
	if err := validateLinkPolicy(binary, target); err != nil {
		return err
	}
	return nil
}

func validateLinkPolicy(binary []byte, target Target) error {
	switch target.GOOS {
	case "linux":
		file, err := elf.NewFile(bytes.NewReader(binary))
		if err != nil {
			return fmt.Errorf("read Linux executable: %w", err)
		}
		defer func() { _ = file.Close() }()
		for _, program := range file.Progs {
			if program.Type == elf.PT_INTERP {
				return fmt.Errorf("linux executable has a dynamic interpreter")
			}
		}
		libraries, err := file.ImportedLibraries()
		if err != nil {
			return fmt.Errorf("inspect Linux imports: %w", err)
		}
		if len(libraries) != 0 {
			return fmt.Errorf("linux executable depends on dynamic libraries: %v", libraries)
		}
	case "darwin":
		file, err := macho.NewFile(bytes.NewReader(binary))
		if err != nil {
			return fmt.Errorf("read Darwin executable: %w", err)
		}
		defer func() { _ = file.Close() }()
		libraries, err := file.ImportedLibraries()
		if err != nil {
			return fmt.Errorf("inspect Darwin imports: %w", err)
		}
		for _, library := range libraries {
			if !strings.HasPrefix(library, "/usr/lib/") && !strings.HasPrefix(library, "/System/Library/Frameworks/") {
				return fmt.Errorf("darwin executable depends on non-system library %q", library)
			}
		}
	case "windows":
		file, err := pe.NewFile(bytes.NewReader(binary))
		if err != nil {
			return fmt.Errorf("read Windows executable: %w", err)
		}
		defer func() { _ = file.Close() }()
		libraries, err := file.ImportedLibraries()
		if err != nil {
			return fmt.Errorf("inspect Windows imports: %w", err)
		}
		allowed := map[string]bool{
			"advapi32.dll": true, "bcrypt.dll": true, "crypt32.dll": true,
			"iphlpapi.dll": true, "kernel32.dll": true, "ntdll.dll": true,
			"secur32.dll": true, "shell32.dll": true, "user32.dll": true,
			"ws2_32.dll": true,
		}
		for _, library := range libraries {
			if !allowed[strings.ToLower(library)] {
				return fmt.Errorf("windows executable depends on unexpected library %q", library)
			}
		}
	}
	return nil
}

func ldflagsSetVersion(ldflags, version string) bool {
	fields := strings.Fields(strings.Trim(ldflags, `"`))
	return ldflagSets(fields, "main.version="+version) &&
		ldflagSets(fields, "main.releaseVersionMarker=gograph-release-version=/"+version+"/")
}

func ldflagSets(fields []string, want string) bool {
	for index, field := range fields {
		if field == "-X" && index+1 < len(fields) && fields[index+1] == want {
			return true
		}
		if strings.HasPrefix(field, "-X=") && strings.TrimPrefix(field, "-X=") == want {
			return true
		}
	}
	return false
}

func targetForBinary(binary []byte) (Target, error) {
	info, err := buildinfo.Read(bytes.NewReader(binary))
	if err != nil {
		return Target{}, fmt.Errorf("read bundled Go build info: %w", err)
	}
	var goos, goarch string
	for _, setting := range info.Settings {
		switch setting.Key {
		case "GOOS":
			goos = setting.Value
		case "GOARCH":
			goarch = setting.Value
		}
	}
	if target, ok := TargetFor(goos, goarch); ok {
		return target, nil
	}
	return Target{}, fmt.Errorf("bundled Go build target %s/%s is unsupported", goos, goarch)
}

func validateLicense(license []byte) error {
	if len(license) == 0 || len(license) > maxLicenseSize {
		return fmt.Errorf("LICENSE size %d is outside allowed range", len(license))
	}
	text := string(license)
	if !strings.HasPrefix(text, "MIT License\n") || !strings.Contains(text, "Copyright (c) 2026 ozgurcd") {
		return fmt.Errorf("LICENSE is not the gograph MIT license")
	}
	return nil
}

func safeArchiveName(name string) bool {
	if name == "" || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
		return false
	}
	return path.Clean(name) == name && !strings.HasPrefix(name, "../")
}

func readZIPEntry(file *zip.File, limit int64) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("open MCPB entry %q: %w", file.Name, err)
	}
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read MCPB entry %q: %w", file.Name, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("MCPB entry %q exceeds size limit", file.Name)
	}
	if uint64(len(data)) != file.UncompressedSize64 {
		return nil, fmt.Errorf("MCPB entry %q size does not match ZIP metadata", file.Name)
	}
	return data, nil
}
