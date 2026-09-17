// Black-box tests for Raft replication (single-node cluster).
package tests

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/c0ldheat/jario/internal/raft"
	"github.com/c0ldheat/jario/internal/store"
)

// newTestNode creates a single-node Raft cluster for testing.
func newTestNode(t *testing.T) (*raft.RaftNode, *store.MetaStore) {
	t.Helper()
	meta, err := store.NewMetaStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewMetaStore: %v", err)
	}
	node, err := raft.New("test-node", t.TempDir(), "localhost:0", meta)
	if err != nil {
		t.Fatalf("raft.New: %v", err)
	}
	t.Cleanup(func() { node.Close() })

	// Bootstrap single-node cluster.
	if err := node.Bootstrap(); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	// Wait for leader election.
	time.Sleep(500 * time.Millisecond)
	if !node.IsLeader() {
		t.Fatal("single-node raft should be leader after bootstrap")
	}
	return node, meta
}

func TestRaftApplyPutObject(t *testing.T) {
	node, meta := newTestNode(t)

	meta.CreateBucket("images")
	objMeta := store.ObjectMeta{Key: "photo.jpg", Size: 1024, ETag: "abc123", Sha256: "abc123", CreatedAt: time.Now()}
	metaJSON, _ := json.Marshal(objMeta)

	if err := node.Apply(raft.Op{Kind: "put_object", Bucket: "images", Key: "photo.jpg", Meta: metaJSON}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	obj, err := meta.GetObject("images", "photo.jpg")
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	if obj.Size != 1024 {
		t.Fatalf("expected size 1024, got %d", obj.Size)
	}
}

func TestRaftApplyDeleteObject(t *testing.T) {
	node, meta := newTestNode(t)

	meta.CreateBucket("data")
	meta.PutObject("data", "file.txt", store.ObjectMeta{Key: "file.txt", Sha256: "aaa"})

	if err := node.Apply(raft.Op{Kind: "delete_object", Bucket: "data", Key: "file.txt"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	_, err := meta.GetObject("data", "file.txt")
	if err == nil || !strings.Contains(err.Error(), "NoSuchKey") {
		t.Fatalf("expected NoSuchKey, got %v", err)
	}
}

func TestRaftSnapshotRestore(t *testing.T) {
	// Test MetaStore snapshot/restore directly — this is what the FSM uses.
	meta, err := store.NewMetaStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewMetaStore: %v", err)
	}
	meta.CreateBucket("snap-bucket")
	meta.PutObject("snap-bucket", "doc.txt", store.ObjectMeta{
		Key: "doc.txt", Size: 50, ETag: "snap1", Sha256: "snap1", CreatedAt: time.Now(),
	})

	// Snapshot.
	var buf strings.Builder
	if err := meta.Snapshot(&buf); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// Restore into a fresh MetaStore.
	newMeta, err := store.NewMetaStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewMetaStore: %v", err)
	}
	// Create the bucket in the new store so Restore can merge objects into it.
	newMeta.CreateBucket("snap-bucket")
	if err := newMeta.Restore(strings.NewReader(buf.String())); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	// Verify restored state.
	obj, err := newMeta.GetObject("snap-bucket", "doc.txt")
	if err != nil {
		t.Fatalf("restored GetObject: %v", err)
	}
	if obj.Size != 50 {
		t.Fatalf("restored size: got %d", obj.Size)
	}
}
