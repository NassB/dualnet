// Package integration holds the tests that need a real Windows machine with
// administrator rights (Wintun adapter, routes, Program Files, registry). They
// are excluded from "go test ./..." by the "integration" build tag and run in
// CI by .github/workflows/integration.yml:
//
//	go test -tags integration -v -count=1 ./integration
package integration
