# jario — Design Spec

**Date:** 2025-08-31
**Status:** Approved (sections 1–3)

## Problem

MinIO's operational complexity (erasure coding knobs, external KMS, pool topology, healing daemons, AGPL licensing) makes it painful to run for small teams and single-node deployments. jario is a minimal S3-compatible object storage server that delivers full S3 API compatibility with near-zero configuration.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Deployment | Both single-node and distributed | Single-node is a 1-node Raft cluster |
| S3 compatibility | Full (SigV4, multipart, versioning) | Existing SDKs/CLIs work unmodified |
| Replication | Plain replication (2x or 3x) | Simplest correct HA; RS deferred |
| Consensus | Embedded Raft (hashicorp/raft) | No external dependencies |
| Language | Go | Strong S3 + Raft ecosystem |

## Architecture

```
┌─────────────────────────────────────┐
│           cmd/jar/main.go           │
│   CLI flags → wire layers → start   │
└──────────────┬──────────────────────┘
               │
    ┌──────────▼──────────┐
    │   internal/api/      │  S3 HTTP handler
    │   SigV4, routing,    │  Port 9000
    │   multipart, errors  │
    └──────────┬───────────┘
               │
    ┌──────────▼──────────┐
    │   internal/store/    │  Storage engine
    │   Content-addressed  │  Blobs on disk
    │   blobs + in-memory  │  Metadata in memory
    │   metadata           │
    └──────────┬───────────┘
               │
    ┌──────────▼──────────┐
    │   internal/raft/     │  Consensus
    │   hashicorp/raft     │  Metadata replication
    │   FSM, snapshots     │  Port 7946
    └─────────────────────┘
```

## Components

### cmd/jar/main.go
CLI entrypoint. Flags: `--data-dir`, `--node-id`, `--peers`, `--rf`, `--port`, `--raft-port`, `--tls-cert`, `--tls-key`. Wires layers, starts server, handles graceful shutdown.

### internal/store/
Storage engine. Content-addressed blobs at `<data-dir>/blobs/<sha256[0:2]>/<sha256>`. In-memory metadata map, backed by Raft FSM. Exposes PutObject, GetObject, DeleteObject, ListObjects, CreateBucket, DeleteBucket, HeadObject.

### internal/raft/
Consensus wrapper around hashicorp/raft. Exposes `Apply(cmd) → result`. FSM applies metadata mutations to store. Handles bootstrap via `--peers`. Snapshots to `<data-dir>/raft/` every 10k entries.

### internal/api/
S3 HTTP handler. SigV4 middleware (existing Go library). Routes for bucket ops, object ops, multipart lifecycle. On PUT: store blob → replicate to RF-1 followers → raft.Apply metadata → 200. On GET: serve local blob or proxy to peer.

### internal/api/errors.go
S3 XML error responses. Maps internal errors to S3 error codes (NoSuchBucket, NoSuchKey, etc.).

### internal/api/health.go
`GET /health` returns 200 with Raft leader status and store health.

### internal/config/config.go
TOML config file support. Flags override config values.

## Data Model

**Bucket:** name, created_at

**Object metadata:** bucket, key, sha256, size, etag (= sha256 hex), content_type, created_at, version_id

**Blob on disk:** `<data-dir>/blobs/<shard>/<sha256>`

**Multipart session:** upload_id, bucket, key, parts map, created_at

## Auth

Single access key / secret key pair via env vars (`JARIO_ACCESS_KEY`, `JARIO_SECRET_KEY`) or flags. No IAM system.

## S3 Operations (v1)

| Operation | Endpoint |
|-----------|----------|
| CreateBucket | PUT /{bucket} |
| DeleteBucket | DELETE /{bucket} |
| ListBuckets | GET / |
| PutObject | PUT /{bucket}/{key} |
| GetObject | GET /{bucket}/{key} |
| DeleteObject | DELETE /{bucket}/{key} |
| HeadObject | HEAD /{bucket}/{key} |
| ListObjectsV2 | GET /{bucket}?list-type=2 |
| CreateMultipartUpload | POST /{bucket}/{key}?uploads |
| UploadPart | PUT /{bucket}/{key}?partNumber=N&uploadId=ID |
| CompleteMultipartUpload | POST /{bucket}/{key}?uploadId=ID |
| AbortMultipartUpload | DELETE /{bucket}/{key}?uploadId=ID |

## Deferred (YAGNI)

- Erasure coding (RS) — replication covers HA
- IAM users/roles/policies — single key pair sufficient
- Object tagging, lock, retention — not requested
- Bucket policies/ACLs — not requested
- TLS termination proxy — native TLS flags suffice
- Docker, CI/CD, observability stack — not requested
- Object versioning config — always-on versioning via version_id field

## Testing

One test file per layer:
- `internal/store/store_test.go` — blob roundtrip
- `internal/raft/raft_test.go` — apply + snapshot
- `internal/api/api_test.go` — S3 handler with httptest

## License

Apache 2.0 (per LICENSE already in repo).
