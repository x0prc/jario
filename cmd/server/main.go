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
	"github.com/c0ldheat/jario/internal/raft"
	"github.com/c0ldheat/jario/internal/store"
)

func main() {
	dataDir := flag.String("data-dir", "./data", "root directory for blobs and metadata")
	listen := flag.String("listen", ":9000", "HTTP listen address (host:port)")
	nodeID := flag.String("node-id", "node1", "unique node identifier for Raft cluster")
	raftAddr := flag.String("raft-addr", "localhost:9090", "Raft TCP transport bind address")
	tlsCert := flag.String("tls-cert", "", "path to TLS certificate file (optional)")
	tlsKey := flag.String("tls-key", "", "path to TLS private key file (optional)")
	accessKey := flag.String("access-key", "minioadmin", "S3 access key for SigV4 auth")
	secretKey := flag.String("secret-key", "minioadmin", "S3 secret key for SigV4 auth")
	flag.Parse()

	// Shared metadata store — Raft FSM and Store both reference this.
	meta := store.NewMetaStore()

	// Storage engine.
	st := store.NewWithMeta(*dataDir, meta)

	// Raft node — FSM applies mutations to the shared meta.
	raftDir := *dataDir + "/raft"
	raftNode, err := raft.New(*nodeID, raftDir, *raftAddr, meta)
	if err != nil {
		log.Fatalf("raft init: %v", err)
	}
	defer raftNode.Close()

	// Wire store to Raft — metadata mutations now go through consensus.
	st.SetRaft(raftNode)

	// Wait for leader election (single-node bootstraps instantly).
	time.Sleep(500 * time.Millisecond)
	if !raftNode.IsLeader() {
		log.Println("warning: this node is not the Raft leader — writes will fail until a leader is elected")
	}

	// HTTP server.
	handler := api.NewHandler(st, *accessKey, *secretKey)
	srv := &http.Server{
		Addr:         *listen,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server (TLS or plain).
	errCh := make(chan error, 1)
	go func() {
		if *tlsCert != "" && *tlsKey != "" {
			log.Printf("jario listening on %s (TLS)", *listen)
			errCh <- srv.ListenAndServeTLS(*tlsCert, *tlsKey)
		} else {
			log.Printf("jario listening on %s", *listen)
			errCh <- srv.ListenAndServe()
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
