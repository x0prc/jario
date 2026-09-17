// Package config loads the single TOML config file.
// Precedence: built-in defaults < config file < explicitly-set flags.
package config

import (
	"fmt"

	"github.com/BurntSushi/toml"
)

// Config mirrors the server CLI flags.
//
// Example file:
//
//	data_dir = "./data"
//	listen = ":9000"
//	node_id = "node1"
//	raft_addr = "localhost:9090"
//	tls_cert = ""
//	tls_key = ""
//	access_key = "minioadmin"
//	secret_key = "minioadmin"
//	bootstrap = false
//	join = ""
//	region = "us-east-1"
type Config struct {
	DataDir   string `toml:"data_dir"`
	Listen    string `toml:"listen"`
	NodeID    string `toml:"node_id"`
	RaftAddr  string `toml:"raft_addr"`
	TLSCert   string `toml:"tls_cert"`
	TLSKey    string `toml:"tls_key"`
	AccessKey string `toml:"access_key"`
	SecretKey string `toml:"secret_key"`
	Bootstrap bool   `toml:"bootstrap"`
	Join      string `toml:"join"`
	Region    string `toml:"region"`
}

// Default returns the zero-config dev defaults (same as flag defaults).
func Default() Config {
	return Config{
		DataDir:   "./data",
		Listen:    ":9000",
		NodeID:    "node1",
		RaftAddr:  "localhost:9090",
		AccessKey: "minioadmin",
		SecretKey: "minioadmin",
		Region:    "us-east-1",
	}
}

// Load returns defaults with the TOML file decoded over them.
// Empty path means "no config file" — just defaults, no error.
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return Config{}, fmt.Errorf("load config %s: %w", path, err)
	}
	return cfg, nil
}
