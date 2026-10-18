package main

// version is set at build time from the VERSION file via
// -ldflags "-X main.version=...". Plain "go build" reports "dev".
var version = "dev"
