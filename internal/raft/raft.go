// Raft consensus layer.
// FSM applies metadata commands to the store's metaStore.
// Every bucket/object metadata mutation goes through Raft.
package raft

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/c0ldheat/jario/internal/store"
	"github.com/hashicorp/raft"
)

// Op is an alias for store.RaftOp — the metadata mutation replicated through Raft.
type Op = store.RaftOp

// RaftNode wraps hashicorp/raft. One per process.
type RaftNode struct {
	raft *raft.Raft
	fsm  *fsm
}

// New creates a Raft node sharing the given MetaStore with the caller.
func New(nodeID, raftDir, bind string, meta *store.MetaStore) (*RaftNode, error) {
	fsm := newFSM(meta)
	config := raft.DefaultConfig()
	config.LocalID = raft.ServerID(nodeID)
	config.SnapshotInterval = 120 * time.Second
	config.SnapshotThreshold = 8192

	transport, err := raft.NewTCPTransport(bind, nil, 3, 10*time.Second, os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("raft transport: %w", err)
	}

	snapshots, err := raft.NewFileSnapshotStore(raftDir, 3, os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("raft snapshots: %w", err)
	}

	stable := raft.NewInmemStore()
	logStore, err := raft.NewLogCache(512, stable)
	if err != nil {
		return nil, fmt.Errorf("raft log store: %w", err)
	}

	r, err := raft.NewRaft(config, fsm, logStore, stable, snapshots, transport)
	if err != nil {
		return nil, fmt.Errorf("raft new: %w", err)
	}

	return &RaftNode{raft: r, fsm: fsm}, nil
}

// Apply replicates a metadata operation. Blocks until committed.
func (n *RaftNode) Apply(op Op) error {
	data, err := json.Marshal(op)
	if err != nil {
		return fmt.Errorf("marshal op: %w", err)
	}
	future := n.raft.Apply(data, 10*time.Second)
	if err := future.Error(); err != nil {
		return fmt.Errorf("raft apply: %w", err)
	}
	return nil
}

// Close shuts down the Raft node gracefully.
func (n *RaftNode) Close() error {
	return n.raft.Shutdown().Error()
}

// IsLeader returns true if this node is the current Raft leader.
func (n *RaftNode) IsLeader() bool {
	return n.raft.State() == raft.Leader
}

// Meta returns the FSM's metaStore for reading.
func (n *RaftNode) Meta() *store.MetaStore {
	return n.fsm.meta
}
