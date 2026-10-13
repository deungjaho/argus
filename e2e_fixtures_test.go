//go:build integration

package main

import (
	"net/http/httptest"
	"testing"
)

// startE2EFixtureServer creates and starts an httptest.Server serving all HTML testbeds and API endpoints.
// It registers its teardown via t.Cleanup().
func startE2EFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(newFixtureMux())
	t.Cleanup(func() {
		server.Close()
	})
	return server
}
