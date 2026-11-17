// Package gograph embeds repository-level documents that the gograph binary
// serves at run time. The command lives in cmd/gograph.
package gograph

import _ "embed"

// ReleaseNotes is RELEASE_NOTES.md as shipped with this source tree, read by
// `gograph whatsnew`. go:embed cannot reach parent directories, so the file
// is embedded from the module root.
//
//go:embed RELEASE_NOTES.md
var ReleaseNotes string
