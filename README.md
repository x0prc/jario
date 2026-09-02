# jario

data and memory containerisation for models

## features

- **S3-compatible API** — bucket ops, object ops, multipart uploads, SigV4 auth
- **single-node or distributed** — same binary, zero config for dev, Raft for clusters
- **plain replication** — no erasure coding knobs, no healing daemons, no KES
- **content-addressed blobs** — deduplication free, idempotent writes
- **embedded Raft** — metadata consensus without external etcd/consul
- **TLS optional** — plain HTTP for dev, `--tls-cert`/`--tls-key` for prod
- **TOML config** — one file, flags override

## performance

- content-addressed storage: O(1) writes, idempotent by sha256
- in-memory metadata: O(1) bucket/object lookups
- no external DB, no external consensus service
- single binary, single data dir

## tests

```bash
go test ./...
```

- `internal/store` — bucket CRUD, object CRUD, prefix listing
- `internal/api` — S3 XML responses, auth stub, routing
- `internal/raft` — apply, snapshot, FSM
