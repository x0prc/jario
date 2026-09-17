# jario

data and memory containerisation for models

## features

- **S3-compatible API** — bucket ops, object ops, multipart uploads, SigV4 auth
- **single-node or distributed** — same binary, `--bootstrap` for dev, `--join` for clusters
- **plain replication** — no erasure coding knobs, no healing daemons, no KES
- **content-addressed blobs** — deduplication free, idempotent writes
- **embedded Raft** — metadata consensus without external etcd/consul
- **persistent storage** — BoltDB-backed Raft log/stable and bucket metadata; survives restarts
- **per-bucket region** — `--region` flag (default: `us-east-1`)
- **TLS optional** — plain HTTP for dev, `--tls-cert`/`--tls-key` for prod
- **TOML config** — one file, flags override

## quick start

```bash
# single-node dev
go run ./cmd/server --bootstrap

# cluster: node 1 bootstraps, node 2 joins
go run ./cmd/server --node-id node1 --bootstrap
go run ./cmd/server --node-id node2 --join http://node1:9000
```

## config

Flags, TOML file, or both. Flags override the file.

```toml
data_dir   = "./data"
listen     = ":9000"
node_id    = "node1"
raft_addr  = "localhost:9090"
access_key = "minioadmin"
secret_key = "minioadmin"
bootstrap  = false
join       = ""
region     = "us-east-1"
```

## tests

```bash
go test ./tests/ -count=1
```

All tests live in `tests/` (black-box, one package):

- **api** — bucket/object ops, HeadBucket, NoSuchBucket errors, SigV4, ListObjectsV2 pagination
- **multipart** — full create/upload/complete cycle, abort, list parts, list uploads, error paths
- **raft** — single-node apply, snapshot/restore, join endpoint
- **store** — bucket CRUD, object CRUD, prefix listing, pagination
- **config** — defaults, file load, missing-file error
