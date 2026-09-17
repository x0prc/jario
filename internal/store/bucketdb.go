// BoltDB-backed bucket metadata. Bucket CRUD lives here and is
// local-only — it never goes through Raft because each node stores
// its own bucket list in BoltDB and object metadata is what gets
// replicated.
package store

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

var bucketsBucket = []byte("buckets")

// BucketMeta is the on-disk representation of one bucket.
type BucketMeta struct {
	Name      string    `json:"name"`
	Region    string    `json:"region"`
	CreatedAt time.Time `json:"created_at"`
}

// BucketDB is the BoltDB store for bucket metadata.
type BucketDB struct {
	db *bolt.DB
}

// OpenBucketDB opens (or creates) the bucket metadata database.
func OpenBucketDB(dataDir string) (*BucketDB, error) {
	path := filepath.Join(dataDir, "buckets.db")
	db, err := bolt.Open(path, 0644, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open bucket db: %w", err)
	}
	// Ensure the top-level bucket exists.
	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucketsBucket)
		return err
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("init bucket db: %w", err)
	}
	return &BucketDB{db: db}, nil
}

// Create adds a bucket. ErrBucketExists on duplicate.
func (b *BucketDB) Create(name, region string) error {
	return b.db.Update(func(tx *bolt.Tx) error {
		bkt := tx.Bucket(bucketsBucket)
		if bkt.Get([]byte(name)) != nil {
			return ErrBucketExists
		}
		meta := BucketMeta{Name: name, Region: region, CreatedAt: time.Now()}
		data, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		return bkt.Put([]byte(name), data)
	})
}

// Delete removes a bucket. ErrNoBucket if not found.
func (b *BucketDB) Delete(name string) error {
	return b.db.Update(func(tx *bolt.Tx) error {
		bkt := tx.Bucket(bucketsBucket)
		if bkt.Get([]byte(name)) == nil {
			return ErrNoBucket
		}
		return bkt.Delete([]byte(name))
	})
}

// Get returns the named bucket, or ErrNoBucket.
func (b *BucketDB) Get(name string) (Bucket, error) {
	var out Bucket
	err := b.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(bucketsBucket).Get([]byte(name))
		if data == nil {
			return ErrNoBucket
		}
		var meta BucketMeta
		if err := json.Unmarshal(data, &meta); err != nil {
			return err
		}
		out = Bucket{Name: meta.Name, CreatedAt: meta.CreatedAt}
		return nil
	})
	return out, err
}

// List returns all buckets.
func (b *BucketDB) List() ([]Bucket, error) {
	var out []Bucket
	err := b.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketsBucket).ForEach(func(k, v []byte) error {
			var meta BucketMeta
			if err := json.Unmarshal(v, &meta); err != nil {
				return err
			}
			out = append(out, Bucket{Name: meta.Name, CreatedAt: meta.CreatedAt})
			return nil
		})
	})
	return out, err
}

// Close releases the database.
func (b *BucketDB) Close() error {
	return b.db.Close()
}
