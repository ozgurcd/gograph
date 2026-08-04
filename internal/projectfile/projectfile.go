// Package projectfile reads user-selected configuration without allowing a
// repository-relative path to escape the selected project through links.
package projectfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ozgurcd/gograph/internal/sourcefs"
)

// ReadConfig reads requested, or defaultRelative when requested is empty.
// Relative paths are confined beneath projectRoot. An absolute requested path
// is an explicit operator-selected location, but its final entry must still be
// a regular non-linked file. A missing default is reported as found=false;
// a missing explicitly requested path is an error.
func ReadConfig(projectRoot, requested, defaultRelative string) (data []byte, resolved string, found bool, err error) {
	optional := requested == ""
	selected := requested
	if optional {
		selected = defaultRelative
	}
	if selected == "" {
		return nil, "", false, nil
	}

	var reader *sourcefs.Reader
	var name string
	if filepath.IsAbs(selected) {
		resolved = filepath.Clean(selected)
		reader, err = sourcefs.Open(filepath.Dir(resolved))
		name = filepath.Base(resolved)
	} else {
		name = filepath.Clean(selected)
		if !filepath.IsLocal(name) {
			return nil, "", false, fmt.Errorf("config path must stay inside the selected project: %q", selected)
		}
		resolved = filepath.Join(projectRoot, name)
		reader, err = sourcefs.Open(projectRoot)
	}
	if err != nil {
		return nil, resolved, false, err
	}
	defer func() { _ = reader.Close() }()

	data, err = reader.ReadRegularFile(name)
	if optional && errors.Is(err, os.ErrNotExist) {
		return nil, resolved, false, nil
	}
	if err != nil {
		return nil, resolved, false, err
	}
	return data, resolved, true, nil
}
