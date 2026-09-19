package koutendb

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
)

// Client is concurrency-safe. Do not copy a Client after Dial.
type Client struct {
	gate        chan struct{}
	peers       []string
	opts        Options
	tls         *tls.Config
	connections map[int]*wire
	closed      bool
}

// Dial connects, authenticates and checks wire compatibility before returning.
func Dial(ctx context.Context, peers []string, options Options) (*Client, error) {
	if ctx == nil {
		return nil, ErrInvalidInput
	}
	o, t, err := normalize(peers, options)
	if err != nil {
		return nil, err
	}
	c := &Client{gate: make(chan struct{}, 1), peers: append([]string(nil), peers...), opts: o, tls: t, connections: make(map[int]*wire)}
	c.gate <- struct{}{}
	if _, err = c.ensure(ctx, 0); err != nil {
		c.drop()
		return nil, err
	}
	return c, nil
}
func (c *Client) lock(ctx context.Context) error {
	if c == nil || c.gate == nil {
		return ErrClosed
	}
	if ctx == nil {
		return ErrInvalidInput
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.gate:
		if err := ctx.Err(); err != nil {
			c.unlock()
			return err
		}
		if c.closed {
			c.unlock()
			return ErrClosed
		}
		return nil
	}
}
func (c *Client) unlock() { c.gate <- struct{}{} }
func (c *Client) drop() {
	for n, w := range c.connections {
		w.close()
		delete(c.connections, n)
	}
}

// Close waits for the current bounded operation, closes all peers, and is idempotent.
func (c *Client) Close() error {
	if c == nil || c.gate == nil {
		return nil
	}
	<-c.gate
	defer c.unlock()
	c.drop()
	c.closed = true
	c.opts.Credentials = Credentials{}
	return nil
}
func (c *Client) ensure(ctx context.Context, node int) (*wire, error) {
	if c.closed {
		return nil, ErrClosed
	}
	if node < 0 || node >= len(c.peers) {
		return nil, ErrProtocol
	}
	if w := c.connections[node]; w != nil {
		return w, nil
	}
	dialctx, cancel := context.WithTimeout(ctx, c.opts.ConnectTimeout)
	defer cancel()
	d := &net.Dialer{}
	s, err := d.DialContext(dialctx, "tcp", c.peers[node])
	if err != nil {
		return nil, ioError(ctx, err)
	}
	w := &wire{conn: s}
	success := false
	defer func() {
		if !success {
			w.close()
		}
	}()
	if c.tls != nil {
		config := c.tls.Clone()
		if config.ServerName == "" {
			config.ServerName, _, _ = net.SplitHostPort(c.peers[node])
		}
		t := tls.Client(s, config)
		w.conn = t
		if err = t.HandshakeContext(dialctx); err != nil {
			return nil, ioError(ctx, err)
		}
	}
	w.reader = bufio.NewReader(w.conn)
	if err = w.handshake(dialctx, c.opts); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, ErrTimeout
		}
		return nil, err
	}
	c.connections[node] = w
	success = true
	return w, nil
}

// Put stores raw bytes. A write with an unknown outcome is never retried.
func (c *Client) Put(ctx context.Context, ring string, data []byte) (ID, error) {
	return c.PutCodec(ctx, ring, data, Raw)
}
func (c *Client) PutJSON(ctx context.Context, ring string, value any) (ID, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return ID{}, ErrInvalidInput
	}
	return c.PutCodec(ctx, ring, b, JSON)
}
func (c *Client) PutCodec(ctx context.Context, ring string, data []byte, codec Codec) (ID, error) {
	if c == nil || c.gate == nil {
		return ID{}, ErrClosed
	}
	if ring == "" || len(ring) > c.opts.MaxFrameBytes || len(data) > c.opts.MaxFrameBytes-len(ring) || !codec.Valid() {
		return ID{}, ErrInvalidInput
	}
	if err := c.lock(ctx); err != nil {
		return ID{}, err
	}
	defer c.unlock()
	w, err := c.ensure(ctx, 0)
	if err != nil {
		return ID{}, err
	}
	body := append([]byte(ring), data...)
	var attempted bool
	var id ID
	err = w.exchange(ctx, c.opts, fmt.Sprintf("PUTR %d %d 0 %s", len(ring), len(data), codec), body, &attempted, func() error {
		p, err := w.header(c.opts.MaxFrameBytes)
		if err != nil {
			return err
		}
		if err = expect(p, "ID", 7, false); err != nil {
			return err
		}
		id, err = parseFields(p[1:])
		return err
	})
	if err != nil {
		c.drop()
		if attempted && !errors.Is(err, ErrServer) && !errors.Is(err, ErrAuthentication) {
			return ID{}, &Error{kind: ErrIndeterminateWrite.kind, message: ErrIndeterminateWrite.message, cause: err}
		}
	}
	return id, err
}
func readOperation[T any](ctx context.Context, c *Client, op func() (T, error)) (T, error) {
	var zero T
	if err := c.lock(ctx); err != nil {
		return zero, err
	}
	defer c.unlock()
	for attempt := 0; attempt < 2; attempt++ {
		result, err := op()
		if err == nil {
			return result, nil
		}
		c.drop()
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		if attempt == 1 || c.opts.DisableReadRetry || !(errors.Is(err, ErrConnection) || errors.Is(err, ErrTimeout)) {
			return zero, err
		}
	}
	return zero, ErrConnection
}
func (c *Client) Health(ctx context.Context) (string, error) {
	return readOperation(ctx, c, func() (string, error) {
		w, err := c.ensure(ctx, 0)
		if err != nil {
			return "", err
		}
		var result string
		var attempted bool
		err = w.exchange(ctx, c.opts, "HEALTH", nil, &attempted, func() error {
			p, err := w.header(c.opts.MaxFrameBytes)
			if err != nil {
				return err
			}
			if p[0] == "ERR" {
				return ErrServer
			}
			if len(p) < 2 || p[0] != "OK" || !strings.HasPrefix(p[1], "node=") {
				return ErrProtocol
			}
			if _, err = bounded(p[1][5:], len(c.peers)-1); err != nil {
				return err
			}
			result = strings.Join(p[1:], " ")
			return nil
		})
		return result, err
	})
}

// Get returns nil for MISS/GONE; an empty stored value has a non-nil Payload.
func (c *Client) Get(ctx context.Context, id ID) (*Payload, error) { return c.read(ctx, id, nil) }
func (c *Client) Query(ctx context.Context, id ID, selection string) (*Payload, error) {
	return c.read(ctx, id, &selection)
}
func (c *Client) read(ctx context.Context, id ID, selection *string) (*Payload, error) {
	if c == nil || c.gate == nil {
		return nil, ErrClosed
	}
	if !id.valid() || selection != nil && len(*selection) > c.opts.MaxFrameBytes {
		return nil, ErrInvalidInput
	}
	return readOperation(ctx, c, func() (*Payload, error) {
		current, node := id, 0
		for hops := 0; hops <= c.opts.MaxRedirects; hops++ {
			w, err := c.ensure(ctx, node)
			if err != nil {
				return nil, err
			}
			fields := strings.ReplaceAll(current.String(), ":", " ")
			header := "GETID " + fields
			var body []byte
			if selection != nil {
				body = []byte(*selection)
				header = fmt.Sprintf("QRYID %s %d", fields, len(body))
			}
			var result *Payload
			var forwarded, attempted bool
			err = w.exchange(ctx, c.opts, header, body, &attempted, func() error {
				p, err := w.header(c.opts.MaxFrameBytes)
				if err != nil {
					return err
				}
				switch p[0] {
				case "MISS", "GONE":
					return expect(p, p[0], 1, false)
				case "FWD":
					if hops == c.opts.MaxRedirects || len(p) != 7 && len(p) != 8 {
						return ErrProtocol
					}
					current, err = parseFields(p[1:7])
					if err != nil {
						return err
					}
					if len(p) == 8 {
						node, err = bounded(p[7], len(c.peers)-1)
						if err != nil {
							return err
						}
					}
					forwarded = true
					return nil
				default:
					if err := expect(p, "VAL", 4, false); err != nil {
						return err
					}
					if _, err = bounded(p[1], len(c.peers)-1); err != nil {
						return err
					}
					n, err := bounded(p[2], c.opts.MaxFrameBytes)
					if err != nil {
						return err
					}
					codec := Codec(p[3])
					if !codec.Valid() {
						return ErrProtocol
					}
					b, err := w.bytes(n, c.opts.MaxFrameBytes)
					if err != nil {
						return err
					}
					result = &Payload{b, codec}
					return nil
				}
			})
			if err != nil {
				return nil, err
			}
			if !forwarded {
				return result, nil
			}
		}
		return nil, ErrProtocol
	})
}

// GetJSON decodes JSON into dst and returns whether the record exists.
func (c *Client) GetJSON(ctx context.Context, id ID, dst any) (bool, error) {
	p, err := c.Get(ctx, id)
	return decodeJSON(p, err, dst)
}
func (c *Client) QueryJSON(ctx context.Context, id ID, selection string, dst any) (bool, error) {
	p, err := c.Query(ctx, id, selection)
	return decodeJSON(p, err, dst)
}
func decodeJSON(p *Payload, err error, dst any) (bool, error) {
	if err != nil || p == nil {
		return false, err
	}
	if p.Codec != JSON {
		return true, ErrProtocol
	}
	if err := json.Unmarshal(p.Data, dst); err != nil {
		var invalid *json.InvalidUnmarshalError
		if errors.As(err, &invalid) {
			return true, ErrInvalidInput
		}
		return true, ErrProtocol
	}
	return true, nil
}
