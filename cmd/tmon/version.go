package main

// version is injected at build time via
//
//	-ldflags "-X main.version=<VERSION>"
//
// Both the Makefile and goreleaser do this. scripts/bootstrap.sh compares
// `tmon version` against the repo's VERSION file to decide whether the
// cached binary is current.
var version = "dev"
