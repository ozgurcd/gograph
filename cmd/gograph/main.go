// Command gograph is the entrypoint for the gograph CLI tool.
package main

import (
	"os"

	"github.com/ozgurcd/gograph/internal/cli"
	"github.com/ozgurcd/gograph/internal/installation"
)

// version is set at compile time via -ldflags "-X main.version=x.y.z".
// Without ldflags, the version comes from Go build info: the module version
// for `go install ...@vX.Y.Z`, or "dev+<revision>" for a local checkout.
var version = "dev"

// releaseVersionMarker gives artifact validation an exact, dependency-proof
// marker. It is intentionally not a runtime compatibility requirement for
// downstream builders that historically set only main.version.
var releaseVersionMarker = "gograph-release-version=/dev/"

func main() {
	if releaseVersionMarker == "" {
		os.Exit(2)
	}
	cli.Version = installation.RunningVersion(version)
	os.Exit(cli.Run(os.Args[1:]))
}
