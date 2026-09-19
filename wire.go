package koutendb

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/blake2b"
	"golang.org/x/crypto/nacl/secretbox"
)

type wire struct {
	conn   net.Conn
	reader *bufio.Reader
	key    *[32]byte
	plain  []byte
}

func (w *wire) close() {
	_ = w.conn.Close()
	if w.key != nil {
		clear(w.key[:])
	}
	clear(w.plain)
}
func derive(parts ...string) *[32]byte {
	h, _ := blake2b.New256(nil)
	for _, s := range parts {
		_, _ = io.WriteString(h, s)
	}
	var key [32]byte
	copy(key[:], h.Sum(nil))
	return &key
}
func encrypt(key *[32]byte, data []byte) ([]byte, error) {
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, ErrConnection
	}
	return secretbox.Seal(nonce[:], data, &nonce, key), nil
}
func line(read func() (byte, error)) ([]string, error) {
	buf := make([]byte, 0, 128)
	for {
		b, err := read()
		if err != nil {
			return nil, err
		}
		if b == '\n' {
			break
		}
		buf = append(buf, b)
		if len(buf) > maxHeader {
			return nil, ErrProtocol
		}
	}
	if len(buf) > 0 && buf[len(buf)-1] == '\r' {
		buf = buf[:len(buf)-1]
	}
	if len(buf) == 0 {
		return nil, ErrProtocol
	}
	for _, b := range buf {
		if b < 32 || b > 126 {
			return nil, ErrProtocol
		}
	}
	return strings.Split(string(buf), " "), nil
}
func (w *wire) rawBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := io.ReadFull(w.reader, b)
	return b, err
}
func (w *wire) fill(max int) error {
	p, err := line(w.reader.ReadByte)
	if err != nil {
		return err
	}
	if err = expect(p, "SEC", 2, false); err != nil {
		return err
	}
	n, err := bounded(p[1], max+maxHeader+41)
	if err != nil || n < 40 {
		return ErrProtocol
	}
	b, err := w.rawBytes(n)
	if err != nil {
		return err
	}
	var nonce [24]byte
	copy(nonce[:], b[:24])
	plain, ok := secretbox.Open(nil, b[24:], &nonce, w.key)
	if !ok || len(w.plain)+len(plain) > max+maxHeader+1 {
		return ErrProtocol
	}
	w.plain = append(w.plain, plain...)
	return nil
}
func (w *wire) bytes(n, max int) ([]byte, error) {
	if w.key == nil {
		return w.rawBytes(n)
	}
	for len(w.plain) < n {
		if err := w.fill(max); err != nil {
			return nil, err
		}
	}
	b := make([]byte, n)
	copy(b, w.plain[:n])
	clear(w.plain[:n])
	w.plain = w.plain[n:]
	return b, nil
}
func (w *wire) header(max int) ([]string, error) {
	if w.key == nil {
		return line(w.reader.ReadByte)
	}
	return line(func() (byte, error) {
		b, err := w.bytes(1, max)
		if err != nil {
			return 0, err
		}
		return b[0], nil
	})
}
func expect(p []string, tag string, n int, auth bool) error {
	if len(p) > 0 && p[0] == "ERR" {
		if auth {
			return ErrAuthentication
		}
		return ErrServer
	}
	if len(p) != n || p[0] != tag {
		return ErrProtocol
	}
	return nil
}
func deadline(ctx context.Context, d time.Duration) time.Time {
	t := time.Now().Add(d)
	if end, ok := ctx.Deadline(); ok && end.Before(t) {
		return end
	}
	return t
}

// exchange owns cancellation until the entire reply body has been read.
// Wait for a running cancellation callback before reusing the connection.
func (w *wire) exchange(ctx context.Context, o Options, header string, body []byte, attempted *bool, reply func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(header) > maxHeader || strings.ContainsAny(header, "\r\n\x00") || len(body) > o.MaxFrameBytes {
		return ErrInvalidInput
	}
	frame := append([]byte(header+"\n"), body...)
	if w.key != nil {
		cipher, err := encrypt(w.key, frame)
		if err != nil {
			return err
		}
		frame = append([]byte(fmt.Sprintf("SEC %d\n", len(cipher))), cipher...)
	}
	if err := w.conn.SetWriteDeadline(deadline(ctx, o.WriteTimeout)); err != nil {
		return ioError(ctx, err)
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = w.conn.SetDeadline(time.Now()); close(done) })
	defer func() {
		if !stop() {
			<-done
		}
	}()
	for len(frame) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		*attempted = true
		n, err := w.conn.Write(frame)
		if err != nil {
			return ioError(ctx, err)
		}
		if n == 0 {
			return ErrConnection
		}
		frame = frame[n:]
	}
	if err := w.conn.SetReadDeadline(deadline(ctx, o.ReadTimeout)); err != nil {
		return ioError(ctx, err)
	}
	// Do not overwrite an already-fired cancellation deadline with a future one.
	if err := ctx.Err(); err != nil {
		return err
	}
	err := reply()
	if err == nil {
		return nil
	}
	if _, ok := err.(*Error); ok {
		return err
	}
	return ioError(ctx, err)
}
func (w *wire) handshake(ctx context.Context, o Options) error {
	exchange := func(h string) ([]string, error) {
		var p []string
		var attempted bool
		err := w.exchange(ctx, o, h, nil, &attempted, func() (err error) { p, err = w.header(o.MaxFrameBytes); return })
		return p, err
	}
	ack := func(h, tag string, n int, auth bool) ([]string, error) {
		p, err := exchange(h)
		if err == nil {
			err = expect(p, tag, n, auth)
		}
		return p, err
	}
	c := o.Credentials
	if c.Username != "" {
		if c.SecretKey == "" {
			if _, err := ack("AUTH "+c.Username+" "+c.Password, "OK", 2, true); err != nil {
				return err
			}
		} else {
			p, err := ack("AUTHCHAL "+c.Username, "CHAL", 2, true)
			if err != nil {
				return err
			}
			challenge, err := hex.DecodeString(p[1])
			if err != nil || len(challenge) != 32 {
				return ErrProtocol
			}
			key := derive("koutendb-auth-v1\x00box\x00", c.SecretKey)
			msg := []byte("koutendb-auth-v1\n" + c.Username + "\n" + c.Password + "\n" + p[1])
			cipher, err := encrypt(key, msg)
			clear(key[:])
			clear(msg)
			if err != nil {
				return err
			}
			if _, err = ack("AUTHRESP "+hex.EncodeToString(cipher), "OK", 2, true); err != nil {
				return err
			}
			w.key = derive("koutendb-auth-v1\x00transport\x00", p[1], "\x00", c.SecretKey)
		}
	}
	if o.Galaxy != "" {
		if _, err := ack("HELLO "+o.Galaxy, "OK", 2, true); err != nil {
			return err
		}
	}
	p, err := ack("WIREVER", "WIREVER", 2, true)
	if err != nil {
		return err
	}
	if p[1] != "1" {
		return ErrVersionMismatch
	}
	p, err = ack("CODECMETA ON", "OK", 2, false)
	if err != nil {
		return err
	}
	if p[1] != "codec-metadata" {
		return ErrProtocol
	}
	return nil
}
