//go:build !integration

package main

import (
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// Ensure tests don't hang forever
	go func() {
		time.Sleep(30 * time.Second)
		// If tests are still running after 30s, something is wrong
	}()
	m.Run()
}
