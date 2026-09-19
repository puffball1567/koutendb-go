# Native TCP

The root package uses Go's `net`, `crypto/tls` and the Go project's
`golang.org/x/crypto` for the existing BLAKE2b/XSalsa20-Poly1305 challenge protocol.
No cgo, OpenSSL, libsodium or KoutenDB shared library is required.

## Connection and TLS

```go
db, err := kouten.Dial(ctx, []string{"db.example.com:17301"}, kouten.Options{
    ConnectTimeout: 3 * time.Second,
    ReadTimeout:    5 * time.Second,
    WriteTimeout:   5 * time.Second,
    Galaxy: "app",
    Credentials: kouten.Credentials{
        Username:  os.Getenv("KOUTENDB_USER"),
        Password:  os.Getenv("KOUTENDB_PASSWORD"),
        SecretKey: os.Getenv("KOUTENDB_SECRET_KEY"),
    },
    TLS: &kouten.TLSOptions{
        CAFile: "ca.pem",
        ServerName: "db.example.com",
    },
})
```

For token authentication, leave Username empty and set `Credentials.AuthToken`.
For password-only authentication, omit SecretKey. Galaxy must match the server.
Every initial connection and reconnect repeats authentication, WIREVER and
CODECMETA negotiation. The server determines placement; the client implements
no orbit calculation.

TLS uses at least TLS 1.2 with certificate-chain and hostname verification.
An empty CAFile uses system roots; a CAFile adds roots from PEM. An empty
ServerName uses the endpoint's host. `InsecureSkipVerify` is an explicit
development-only opt-in and disables both chain and hostname validation.

Without TLS, restrict connections to localhost or a trusted private/container
network. Never expose a plaintext server to an untrusted network. Secret-key
transport authentication is not a substitute for TLS certificate verification.

Credentials and Options redact ordinary formatted and JSON output. Exceptions
never include server-provided text, endpoint details, credentials or payloads.
Go strings are immutable and garbage-collected: this is not a guarantee that
all credential copies are securely erased from process memory.

## Reading and Selecting

```go
id, err := kouten.ParseID(savedID)
if err != nil { return err }
payload, err := db.Get(ctx, id)
// nil payload with nil error means MISS/GONE; zero Data length is a stored value.

var selected struct { Title string }
found, err := db.QueryJSON(ctx, id, "{ title }", &selected)
```

Persist the full `id.String()` value, including period and head. JSON encoding
of ID is a string, avoiding loss of uint64 precision in JSON consumers.
`GetJSON` and `QueryJSON` require the returned JSON codec. Raw/NIF/BIF bytes are
available through `Get` and `Query` with their codec metadata.

## Failures and Retry

```go
id, err := db.PutJSON(ctx, "articles", article)
switch {
case errors.Is(err, kouten.ErrIndeterminateWrite):
    // Reconcile with application state. Do not repeat the insertion blindly.
case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
    // Request cancellation or caller deadline.
case errors.Is(err, kouten.ErrTimeout):
    // Configured connect/read/write timeout.
case errors.Is(err, kouten.ErrAuthentication):
    // Invalid credentials or galaxy.
case errors.Is(err, kouten.ErrConnection):
    // Connection failure, TLS verification failure, or a closed client.
}
```

Other sentinels: ErrProtocol, ErrVersionMismatch, ErrServer, ErrInvalidInput and
ErrClosed. Use `errors.Is`; indeterminate-write errors may wrap the underlying
context/transport error. Check ErrIndeterminateWrite first.

Writes are never automatically replayed. Once sending begins, interruption,
malformed success responses and missing acknowledgements mean an unknown write
outcome. The driver cannot promise exactly-once delivery without server-side
idempotency. A server ERR is surfaced as ErrServer without exposing its text.

Reads retry once on a connection failure or transport timeout, including a
truncated response body. Set DisableReadRetry to true to disable this behavior.
Protocol/auth/version errors and canceled contexts are not automatically retried.
Damaged connections are closed; later operations establish a fresh connection.
Close is terminal and idempotent. It waits for an active bounded operation;
cancel that operation's context for prompt shutdown.

Peer order must match the server configuration. GETID/QRYID follow only explicit
FWD responses to configured peer indexes. No broadcast scan, arbitrary redirect
host or speculative miss probing is performed. PUTR uses the first peer as the
entry server; the server handles placement. No automatic initial-peer failover.

## Bounds

Zero options select defaults: five-second connect/read/write timeouts, 64 MiB
payload/frame limit and eight redirects. Maximums are one hour per timeout,
64 peers, 64 MiB frames, 32 redirects and an 8 KiB header. MaxFrameBytes may be
lowered. Zero-length payloads are valid. All lengths count bytes, not characters.

ReadTimeout covers the entire response frame, including its body, not each
individual socket read. Context deadlines span retries, redirects and lock waits.
Concurrent callers must not mutate input byte slices until the method returns.
