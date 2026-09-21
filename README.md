# jario

Single-binary S3-compatible object storage with embedded Raft consensus.

## features

- **S3-compatible API** — bucket/object ops, multipart uploads, SigV4 auth
- **object versioning** — every PUT creates a new version; DELETE creates delete markers
- **single-node or distributed** — same binary, `--bootstrap` for dev, `--join` for clusters
- **content-addressed blobs** — sha256 deduplication, idempotent writes
- **embedded Raft** — metadata consensus without external etcd/consul
- **persistent storage** — BoltDB-backed Raft log/stable and bucket metadata
- **per-bucket region** — `--region` flag (default: `us-east-1`)
- **multipart upload cleanup** — background reaper (`--upload-max-age`, default 24h)
- **TLS optional** — plain HTTP for dev, `--tls-cert`/`--tls-key` for prod

## architecture

```
┌──────────────────────────────────────────────────┐
│  S3 API (SigV4 auth)                            │
│  internal/api/api.go                            │
├──────────────┬───────────────────────────────────┤
│              │                                   │
│  Store       │  RaftNode                        │
│  (blob I/O)  │  (consensus)                     │
│              │                                   │
├──────────────┴───────────────────────────────────┤
│  MetaStore                                       │
│  ┌─────────────┐  ┌────────────────────────┐     │
│  │ BucketDB    │  │ Object versions (mem)  │     │
│  │ (BoltDB)    │  │ + Raft snapshot        │     │
│  └─────────────┘  └────────────────────────┘     │
├──────────────────────────────────────────────────┤
│  Blobs on disk: data/blobs/<sha[0:2]>/<sha>      │
└──────────────────────────────────────────────────┘
```

- **Bucket CRUD** is local-only (BoltDB), never replicated.
- **Object metadata** is replicated through Raft consensus.
- **Blobs** are content-addressed and idempotent — safe to retry.

## quick start

```bash
# single-node dev
go run ./cmd/server --bootstrap

# cluster: node 1 bootstraps, node 2 joins
go run ./cmd/server --node-id node1 --bootstrap
go run ./cmd/server --node-id node2 --join http://node1:9000
```

## build

```bash
go build -o jario ./cmd/server
# or
make build
```

## config

Flags, TOML file, or both. Flags override the file.

```toml
data_dir        = "./data"
listen          = ":9000"
node_id         = "node1"
raft_addr       = "localhost:9090"
access_key      = "minioadmin"
secret_key      = "minioadmin"
bootstrap       = false
join            = ""
region          = "us-east-1"
upload_max_age  = "24h"
tls_cert        = ""
tls_key         = ""
```

## S3 API reference

| Operation | Method | Path | Notes |
|---|---|---|---|
| List buckets | GET | `/` | |
| Create bucket | PUT | `/{bucket}` | |
| Head bucket | HEAD | `/{bucket}` | 200/404 |
| Delete bucket | DELETE | `/{bucket}` | |
| List objects v2 | GET | `/{bucket}?list-type=2` | `prefix`, `start-after`, `continuation-token`, `max-keys` |
| List object versions | GET | `/{bucket}?versions` | `prefix` |
| List multipart uploads | GET | `/{bucket}?uploads` | |
| Put object | PUT | `/{bucket}/{key}` | Returns `X-Amz-Version-Id` |
| Get object | GET | `/{bucket}/{key}` | `?versionId=X` for specific version |
| Head object | HEAD | `/{bucket}/{key}` | `?versionId=X` for specific version |
| Delete object | DELETE | `/{bucket}/{key}` | Without `versionId` creates delete marker |
| Delete version | DELETE | `/{bucket}/{key}?versionId=X` | Permanently removes version |
| Initiate multipart | POST | `/{bucket}/{key}?uploads` | |
| Upload part | PUT | `/{bucket}/{key}?uploadId=X&partNumber=N` | |
| Complete multipart | POST | `/{bucket}/{key}?uploadId=X` | |
| List parts | GET | `/{bucket}/{key}?uploadId=X` | |
| Abort multipart | DELETE | `/{bucket}/{key}?uploadId=X` | |

### versioning

Versioning is always enabled. Every `PUT` creates a new version (monotonic integer IDs). `DELETE` without `versionId` creates a delete marker. Specific versions are deleted permanently with `?versionId=X`.

Responses include `X-Amz-Version-Id` and `X-Amz-Delete-Marker` headers where applicable.

## security

- **SigV4 authentication** required on all S3 endpoints.
- **`/internal/join`** bypasses SigV4 — firewall this endpoint in production.
- **Default credentials** are `minioadmin`/`minioadmin` — change them.
- **Request body limit**: 5 GiB per object (S3 standard limit).

## testing

```bash
make test          # standard tests
make test-race     # with race detector
make lint          # golangci-lint
```

All tests live in `tests/` (black-box):

- **api** — bucket/object ops, HeadBucket, NoSuchBucket errors, SigV4, ListObjectsV2, versioning API
- **multipart** — full create/upload/complete cycle, abort, list parts, list uploads, error paths
- **raft** — single-node apply, snapshot/restore, join endpoint
- **store** — bucket CRUD, object versioning, delete markers, prefix listing, pagination
- **config** — defaults, file load, missing-file error
- **join** — cluster membership, leader check, bad body, round-trip

## dependencies

- [hashicorp/raft](https://github.com/hashicorp/raft) — Raft consensus
- [hashicorp/raft-boltdb](https://github.com/hashicorp/raft-boltdb) — BoltDB log/stable store
- [bbolt](https://github.com/etcd-io/bbolt) — bucket metadata persistence
- [BurntSushi/toml](https://github.com/BurntSushi/toml) — config parsing

## known limitations

- No bucket policies or ACLs
- No presigned URLs
- No CORS support
- No lifecycle policies
- Multipart uploads are in-memory (not replicated, lost on restart)
- No background compaction for old object versions

## license

GPL-3.0 — see [LICENSE](LICENSE).
