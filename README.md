# KoutenDB Go Driver

Native Go TCP access and an optional embedded C ABI wrapper for
[KoutenDB](https://github.com/puffball1567/koutendb).

| Mode | Import | Native dependencies |
| --- | --- | --- |
| TCP server | `github.com/puffball1567/koutendb-go` | None; works with `CGO_ENABLED=0` |
| Embedded | `github.com/puffball1567/koutendb-go/embedded` | C compiler and ABI v2 `libkoutendb`; `kouten_embedded` build tag |

Go 1.26 or newer. Both transports preserve raw, JSON, NIF and BIF payloads;
NIF/BIF encoding and decoding remain application responsibilities.

## Native TCP

Install from this checkout before the first release:

```bash
go test ./...
go run ./examples/tcp
```

Once published, applications can add the module with
`go get github.com/puffball1567/koutendb-go`.

Start a local server using the KoutenDB distribution:

```bash
koutend --id=0 --peers=127.0.0.1:17301 --data=./data
```

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    kouten "github.com/puffball1567/koutendb-go"
)

func main() {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    db, err := kouten.Dial(ctx, []string{"127.0.0.1:17301"}, kouten.Options{})
    if err != nil { log.Fatal(err) }
    defer db.Close()

    id, err := db.PutJSON(ctx, "articles", map[string]string{"title": "Hello"})
    if err != nil { log.Fatal(err) }
    var article struct { Title string }
    found, err := db.GetJSON(ctx, id, &article)
    if err != nil { log.Fatal(err) }
    fmt.Println(found, article.Title, id.String())
}
```

Clients serialize requests and can be shared between goroutines. Use multiple
clients for parallel requests. Every operation accepts a context; cancellation
also interrupts in-flight socket I/O and waiting for the client lock.

See [TCP setup, authentication, TLS and errors](docs/native-tcp.md).

## Embedded

Build the shared library from a compatible KoutenDB checkout:

```bash
cd ../koutendb
bash scripts/build_capi.sh
```

From this driver checkout on Linux:

```bash
export CGO_LDFLAGS="-L$(realpath ../koutendb/lib)"
export LD_LIBRARY_PATH="$(realpath ../koutendb/lib)${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
go run -tags=kouten_embedded ./examples/embedded
```

On macOS, build with `KOUTENDB_CAPI_OUT=lib/libkoutendb.dylib bash scripts/build_capi.sh`
and set `DYLD_LIBRARY_PATH` instead of `LD_LIBRARY_PATH`. If the library
is installed in the system loader's search path, these path settings are not
needed. The C header is included in this package.

Embedded supports in-memory and persistent stores, codec-aware CRUD, vector
insertion, JSON selection and ring-page reads. Each handle owns a dedicated OS
thread so C ABI calls and thread-local error capture remain together. Always
call `Close`; no garbage-collection finalizer owns the database lifecycle.

See [embedded API and examples](docs/embedded.md).

## Compatibility and Validation

| Capability | Native TCP | Embedded |
| --- | --- | --- |
| Put, JSON put, get, JSON get, query | Yes | Yes |
| Codec metadata, empty/binary payloads | Yes | Yes |
| Health, authentication, verified TLS | Yes | Not applicable |
| Update / remove | Not yet exposed | Yes |
| Ring filtering, selection, sorting, pagination | Not yet exposed | Yes |
| Vector insertion | Not yet exposed | Yes |
| Administrative APIs / full driver parity | Not yet exposed | Not yet exposed |

The initial native transport targets wire v1; it fails closed on another wire
version. Embedded requires C ABI v2 and the symbols used by this release.
Its four-field ID is intentionally distinct from the six-field TCP ID.

[Validation matrix and commands](docs/validation.md) cover the shared protocol
suite, real authenticated/TLS servers, partial I/O, invalid frames, cancellation,
concurrent operations, persistence and race detection. Linux/macOS CI includes
real-server and C ABI checks; Windows CI covers native Go tests.

## License

Apache-2.0. See [LICENSE](LICENSE).
