# Validation

## Local Results (2026-09-20)

- Native builds and tests passed with Go 1.26.0 and Go 1.27.0 on Linux.
- The shared 27-case fixture suite and all six real-server configurations passed.
- Native and embedded tests passed with the race detector; native concurrency
  and cancellation tests also passed ten consecutive runs.
- Embedded codec/CRUD matrix, persistent reopen, ring reads and both demos passed.
- Ten-second ID and header fuzz runs completed over 440,000 inputs without failure.
- `go vet` passed for native and embedded builds.
- `govulncheck` reported zero affected symbols and zero imported-package
  vulnerabilities with x/crypto v0.57.0 and Go 1.27.0. It still reports the
  module-level OpenPGP advisory GO-2026-5932; this driver does not import OpenPGP.

## CI Results (2026-09-20)

All eight jobs in the [release validation run](https://github.com/puffball1567/koutendb-go/actions/runs/35457854329) passed:

- Linux, macOS and Windows: native tests and cgo-disabled builds on Go 1.26.x
  and the current stable Go release.
- Linux and macOS: shared protocol fixtures, all six real-server configurations,
  and embedded tests and demo.
- Race detection on Linux/macOS; parser fuzzing and vulnerability checks on Linux.

Windows coverage is native-client only; embedded and real-server validation
is currently covered on Linux/macOS.

## Matrix

| Area | Cases |
| --- | --- |
| Protocol | WIREVER negotiation, mismatch, malformed/oversized version replies, CODECMETA |
| Framing | Fragmented Unicode, binary NUL, empty values, bad length/header/codec, send backpressure |
| Routing | Ownerless and cross-node FWD, invalid node index, redirect cycle, no miss broadcast |
| Retry | Read retry once, truncated body retry, exhausted retry, poisoned connection discarded |
| Mutations | Disconnect/timeout/malformed ID after write, cancellation after send, no blind replay |
| Security | Password, token, secret challenge, wrong credentials/galaxy/key, TLS and secret+TLS |
| Certificates | Trusted root, untrusted root, wrong hostname, explicit development insecure mode |
| Go contexts | Waiting for client lock, canceled-before-send, in-flight cancellation |
| Concurrency | Serialized shared client, concurrent close, embedded goroutine access |
| Embedded | Four codecs, empty/Unicode/binary/large payloads, CRUD, reopen, ring filter/selection, vectors |
| Fuzz | Wire ID parser and bounded header parser |

The core shared harness has 27 scripted cases and six real-server configurations.
It runs against fresh temporary data directories and does not reuse an existing
application database. This module adds language-specific boundary, cancellation
and concurrency tests. Race detection checks Go code; it does not instrument the
prebuilt Nim/C shared library.

## Local Commands

```bash
CGO_ENABLED=0 go test ./...
go test -race -count=1 ./...
go vet ./...
go test -run='^$' -fuzz=FuzzParseID -fuzztime=10s .
go test -run='^$' -fuzz=FuzzHeader -fuzztime=10s .
```

After building a TLS-enabled server in a sibling KoutenDB checkout:

```bash
go build -o bin/conformance ./cmd/conformance
python3 ../koutendb/scripts/native_driver_conformance.py \
  --server ../koutendb/src/koutend -- ./bin/conformance
```

Build/link the C ABI as described in the README, then:

```bash
go test -race -tags=kouten_embedded -count=1 ./...
go run -tags=kouten_embedded ./examples/embedded
```

CI is configured for Linux/macOS real-server and embedded validation, native Go
unit tests on Windows, and cgo-disabled builds. No large benchmark or endurance
workload runs in CI.
