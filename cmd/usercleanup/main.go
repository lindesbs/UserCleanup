// Command usercleanup is the entry point of the UserCleanup CLI
package main

import "UserCleanup/internal/usercleanup"

// version is set at build time from the VERSION file via
// -ldflags "-X main.version=...". Plain "go build" reports "dev".
var version = "dev"

func main() {
	usercleanup.Execute(version)
}
