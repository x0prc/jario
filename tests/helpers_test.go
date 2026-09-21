// Shared test helpers.
package tests

import (
	"testing"

	"github.com/c0ldheat/jario/internal/store"
)

func mustNewStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	return s
}
