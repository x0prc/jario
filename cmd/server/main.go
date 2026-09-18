// jario server — single-binary S3-compatible object storage.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/c0ldheat/jario/internal/api"
	"github.com/c0ldheat/jario/internal/config"
	"github.com/c0ldheat/jario/internal/raft"
	"github.com/c0ldheat/jario/internal/store"
)

func main() {
	configPath := flag.String("config", "", "path to TOML config file (optional, flags override file)")
	dataDir := flag.String("data-dir", "./data", "root directory for blobs and metadata")
	listen := flag.String("listen", ":9000", "HTTP listen address (host:port)")
	nodeID := flag.String("node-id", "node1", "unique node identifier for Raft cluster")
	raftAddr := flag.String("raft-addr", "localhost:9090", "Raft TCP transport bind address")
	tlsCert := flag.String("tls-cert", "", "path to TLS certificate file (optional)")
	tlsKey := flag.String("tls-key", "", "path to TLS private key file (optional)")
	accessKey := flag.String("access-key", "minioadmin", "S3 access key for SigV4 auth")
	secretKey := flag.String("secret-key", "minioadmin", "S3 secret key for SigV4 auth")
	bootstrap := flag.Bool("bootstrap", false, "bootstrap a new single-node Raft cluster (first run only)")
	join := flag.String("join", "", "HTTP address of an existing node to join (e.g. http://node1:9000)")
	region := flag.String("region", "us-east-1", "S3 region for new buckets")
	uploadMaxAge := flag.String("upload-max-age", "24h", "max age for incomplete multipart uploads before auto-abort")
	flag.Parse()

	// Precedence: defaults < config file < explicitly-set flags.
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	// Only flags passed on the command line override the file.
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "data-dir":
			cfg.DataDir = *dataDir
		case "listen":
			cfg.Listen = *listen
		case "node-id":
			cfg.NodeID = *nodeID
		case "raft-addr":
			cfg.RaftAddr = *raftAddr
		case "tls-cert":
			cfg.TLSCert = *tlsCert
		case "tls-key":
			cfg.TLSKey = *tlsKey
		case "access-key":
			cfg.AccessKey = *accessKey
		case "secret-key":
			cfg.SecretKey = *secretKey
		case "bootstrap":
			cfg.Bootstrap = *bootstrap
		case "join":
			cfg.Join = *join
		case "region":
			cfg.Region = *region
		case "upload-max-age":
			cfg.UploadMaxAge = *uploadMaxAge
		}
	})

	if cfg.Bootstrap && cfg.Join != "" {
		log.Fatal("--bootstrap and --join are mutually exclusive")
	}

	// Shared metadata store — Raft FSM and Store both reference this.
	meta, err := store.NewMetaStore(cfg.DataDir)
	if err != nil {
		log.Fatalf("meta store: %v", err)
	}
	meta.SetRegion(cfg.Region)

	// Storage engine.
	st := store.NewWithMeta(cfg.DataDir, meta)

	// Raft node — FSM applies mutations to the shared meta.
	raftDir := cfg.DataDir + "/raft"
	raftNode, err := raft.New(cfg.NodeID, raftDir, cfg.RaftAddr, meta)
	if err != nil {
		log.Fatalf("raft init: %v", err)
	}
	defer raftNode.Close()

	// Wire store to Raft — metadata mutations now go through consensus.
	st.SetRaft(raftNode)

	// Bootstrap single-node cluster on first run.
	if cfg.Bootstrap {
		if err := raftNode.Bootstrap(); err != nil {
			log.Fatalf("raft bootstrap: %v", err)
		}
	}

	// Join an existing cluster: ask it to admit us, retrying until it does.
	// Our Raft node picks up the replicated configuration automatically.
	if cfg.Join != "" {
		for {
			err := api.RequestJoin(cfg.Join, cfg.NodeID, cfg.RaftAddr)
			if err == nil {
				log.Printf("joined cluster via %s", cfg.Join)
				break
			}
			log.Printf("join failed (%v), retrying...", err)
			time.Sleep(2 * time.Second)
		}
	}

	// Wait for leader election (single-node bootstraps instantly).
	time.Sleep(500 * time.Millisecond)
	if !raftNode.IsLeader() {
		log.Println("warning: this node is not the Raft leader — writes will fail until a leader is elected")
	}

	// HTTP server.
	handler := api.NewHandler(st, cfg.AccessKey, cfg.SecretKey)
	// Admit joiners into the Raft cluster.
	if h, ok := handler.(interface{ SetJoiner(api.Joiner) }); ok {
		h.SetJoiner(raftNode)
	}
	srv := &http.Server{
		Addr:         cfg.Listen,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server (TLS or plain).
	errCh := make(chan error, 1)
	go func() {
		if cfg.TLSCert != "" && cfg.TLSKey != "" {
			log.Printf("jario listening on %s (TLS)", cfg.Listen)
			errCh <- srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
		} else {
			log.Printf("jario listening on %s", cfg.Listen)
			errCh <- srv.ListenAndServe()
		}
	}()

	// Background reaper: abort stale incomplete multipart uploads.
	maxAge, err := time.ParseDuration(cfg.UploadMaxAge)
	if err != nil {
		log.Fatalf("invalid upload-max-age %q: %v", cfg.UploadMaxAge, err)
	}
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			if n := st.AbortStaleUploads(maxAge); n > 0 {
				log.Printf("reaper: aborted %d stale multipart uploads", n)
			}
		}
	}()

	// Graceful shutdown on SIGINT/SIGTERM.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("received %s, shutting down...", sig)
	case err := <-errCh:
		if err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "shutdown error: %v\n", err)
	}
	log.Println("jario stopped")
}
