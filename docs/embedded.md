# Embedded API

Import `github.com/puffball1567/koutendb-go/embedded` with cgo and the
`kouten_embedded` build tag. See the README for linking instructions.

```go
db, err := embedded.OpenWithOptions(embedded.OpenOptions{
    Nodes: 8,
    Directory: "./data",
    StrongDurability: true,
    DiskBacked: true,
})
if err != nil { return err }
defer db.Close()

id, err := db.PutJSON("users/123/profile", map[string]any{
    "name": "Ada", "active": true,
})
if err != nil { return err }
page, err := db.ReadRing("users/123/profile", embedded.RingOptions{
    Filter: json.RawMessage(`{"active":true}`),
    Selection: "{ name }",
    Pagination: true,
    Page: 1,
    PageLimit: 20,
    Sort: "name",
})
```

- `Open(nodes)`: in-memory store.
- `OpenDir(nodes, directory)`: persistent store with core defaults.
- `OpenWithOptions`: persistence, durability and disk-backed read mode.
- `Put`, `PutJSON`, `PutCodec`, `PutVec`: insertion with explicit payload codec.
- `Get`, `GetJSON`, `Query`: ID access and JSON projection.
- `Update`, `Remove`: mutate a stored record.
- `ReadRing`: stable page JSON, filtering, projection, sorting and pagination.
- `Close`: explicit, serialized and idempotent handle release.

ReadRing returns the core response unchanged, including item IDs, page metadata
and binary encoding markers. Limit zero follows core unlimited/default semantics;
use a positive limit for bounded reads. With pagination, zero Page/PageLimit are
normalized to 1/20. See the core documentation for the detailed page contract.

Get returns a nil payload for a missing record; an empty value is non-nil.
Errors use root-package ErrInvalidInput/ErrClosed/ErrVersionMismatch and
embedded.ErrABI/ErrNotFound. Raw C ABI error strings are not exposed because
they may contain application data. Embedded ID has four fields; it is not a
TCP wire ID and must not be converted by guessing period/head.

Calls are serialized on a dedicated locked OS thread for each handle. Different
handles have separate workers. Close waits for an active ABI operation and
releases the worker; Go contexts cannot safely cancel a synchronous C ABI call.
Do not mutate input slices concurrently with calls, and do not copy a DB value.

The package is deliberately not a complete binding of every administrative API.
It does not yet expose vector retrieval or the full transaction/maintenance APIs.
