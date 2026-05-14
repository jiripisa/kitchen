// Package version exposes build-time metadata.
//
// Values are overridden at build time via -ldflags. See .goreleaser.yaml.
package version

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// IsDev reports whether this is an unstamped development build.
func IsDev() bool {
	return Version == "dev"
}
