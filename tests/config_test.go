// Black-box tests for TOML config loading.
package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/c0ldheat/jario/internal/config"
)

func TestConfigDefault(t *testing.T) {
	cfg := config.Default()
	if cfg.DataDir != "./data" || cfg.Listen != ":9000" || cfg.NodeID != "node1" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestConfigLoadEmptyPath(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg != config.Default() {
		t.Fatalf("expected defaults, got %+v", cfg)
	}
}

func TestConfigLoadPartialFileKeepsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jario.toml")
	body := "node_id = \"node2\"\nbootstrap = true\n"
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.NodeID != "node2" || !cfg.Bootstrap {
		t.Fatalf("file values not applied: %+v", cfg)
	}
	// Unset keys keep defaults.
	if cfg.DataDir != "./data" || cfg.Listen != ":9000" {
		t.Fatalf("defaults lost: %+v", cfg)
	}
}

func TestConfigLoadMissingFile(t *testing.T) {
	if _, err := config.Load(filepath.Join(t.TempDir(), "nope.toml")); err == nil {
		t.Fatal("expected error for missing file")
	}
}
