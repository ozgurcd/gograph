// Command gograph is the entrypoint for the gograph CLI tool.
package main

import (
	"os"

	"github.com/ozgurcd/gograph/internal/cli"
)

// version is set at compile time via -ldflags "-X main.version=x.y.z".
// Falls back to "dev" when built without ldflags.
var version = "dev"

// releaseVersionMarker gives artifact validation an exact, dependency-proof
// marker. It is intentionally not a runtime compatibility requirement for
// downstream builders that historically set only main.version.
var releaseVersionMarker = "gograph-release-version=/dev/"

func main() {
	if releaseVersionMarker == "" {
		os.Exit(2)
	}
	if version != "dev" {
		cli.Version = version
	}
	os.Exit(cli.Run(os.Args[1:]))
}
