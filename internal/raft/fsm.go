// Raft FSM — applies committed commands to metaStore.
package raft

import (
	"encoding/json"
	"io"

	"github.com/c0ldheat/jario/internal/store"
	"github.com/hashicorp/raft"
)

// fsm implements raft.FSM. All mutations must be deterministic.
type fsm struct {
	meta *store.MetaStore
}

func newFSM(meta *store.MetaStore) *fsm {
	return &fsm{meta: meta}
}

// Apply is called by Raft on every committed log entry.
// Returns error to the Apply caller if the command fails.
func (f *fsm) Apply(log *raft.Log) interface{} {
	var op Op
	if err := json.Unmarshal(log.Data, &op); err != nil {
		return err
	}
	return store.ApplyOp(f.meta, op)
}

// Snapshot captures current state for Raft log compaction.
func (f *fsm) Snapshot() (raft.FSMSnapshot, error) {
	return &fsmSnapshot{meta: f.meta}, nil
}

// Restore loads a snapshot into the FSM.
func (f *fsm) Restore(rc io.ReadCloser) error {
	defer rc.Close()
	return f.meta.Restore(rc)
}

// fsmSnapshot wraps metaStore for Raft persistence.
type fsmSnapshot struct {
	meta *store.MetaStore
}

// Persist writes the snapshot to the sink.
func (s *fsmSnapshot) Persist(sink raft.SnapshotSink) error {
	if err := s.meta.Snapshot(sink); err != nil {
		sink.Cancel()
		return err
	}
	return sink.Close()
}

// Release is called when the snapshot is no longer needed.
func (s *fsmSnapshot) Release() {}
