package koutendb

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIDBoundaryMatrix(t *testing.T) {
	text := "18446744073709551615:4294967295:4294967295:1.25:60:0.5"
	id, err := ParseID(text)
	if err != nil || id.String() != text {
		t.Fatalf("roundtrip %v %v", id, err)
	}
	b, err := json.Marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	var restored ID
	if json.Unmarshal(b, &restored) != nil || restored != id {
		t.Fatal("JSON identity loses precision")
	}
	for _, s := range []string{"", "1:2:3", "18446744073709551616:1:1:1:60:0", "1:4294967296:1:1:60:0", "1:1:4294967296:1:60:0", "01:1:1:1:60:0", "-1:1:1:1:60:0", "1:1:1:NaN:60:0", "1:1:1:1:0:0", "1:1:1:1:-1:0", "1:1:1:1:60:Inf", "1:1:1:1:1e999:0"} {
		t.Run(s, func(t *testing.T) {
			if _, err := ParseID(s); !errors.Is(err, ErrInvalidInput) {
				t.Fatal(err)
			}
		})
	}
}

func TestZeroClient(t *testing.T) {
	for _, c := range []*Client{nil, {}} {
		if _, err := c.Health(context.Background()); !errors.Is(err, ErrClosed) {
			t.Fatal(err)
		}
		if _, err := c.Put(context.Background(), "r", nil); !errors.Is(err, ErrClosed) {
			t.Fatal(err)
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
func TestLengthAndHeaderMatrix(t *testing.T) {
	for _, s := range []string{"-1", "+1", "01", "1e1", "999999999999999999999999", "11", ""} {
		if _, err := bounded(s, 10); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
	for _, s := range []string{"0", "10"} {
		if _, err := bounded(s, 10); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []string{"\n", "VAL\x00\n", "VAL\t1\n", strings.Repeat("x", maxHeader+1) + "\n"} {
		if _, err := line(bufio.NewReader(strings.NewReader(s)).ReadByte); !errors.Is(err, ErrProtocol) {
			t.Fatalf("header %v", err)
		}
	}
	if p, err := line(bufio.NewReader(strings.NewReader("OK node=0\r\n")).ReadByte); err != nil || len(p) != 2 {
		t.Fatal(p, err)
	}
}
func TestOptionsAndRedaction(t *testing.T) {
	for _, peers := range [][]string{nil, {""}, {"host:0"}, {"host:65536"}, {"host\n:1"}, {"localhost"}, make([]string, 65)} {
		if _, _, err := normalize(peers, Options{}); !errors.Is(err, ErrInvalidInput) {
			t.Fatal(peers, err)
		}
	}
	for _, o := range []Options{{ReadTimeout: -1}, {WriteTimeout: 2 * time.Hour}, {MaxFrameBytes: -1}, {MaxFrameBytes: maxFrame + 1}, {MaxRedirects: 33}, {Credentials: Credentials{Username: "x\n", Password: "secret"}}, {Credentials: Credentials{Password: "secret"}}, {Galaxy: "a b"}, {TLS: &TLSOptions{CAFile: "does-not-exist"}}} {
		if _, _, err := normalize([]string{"localhost:1"}, o); !errors.Is(err, ErrInvalidInput) {
			t.Fatal(err)
		}
	}
	c := Credentials{Username: "private-user", Password: "private-password", SecretKey: "private-secret", AuthToken: "private-token"}
	o := Options{Credentials: c}
	b, _ := json.Marshal(o)
	for _, out := range []string{fmt.Sprintf("%+v %#v", c, c), fmt.Sprintf("%+v %#v", o, o), string(b)} {
		if strings.Contains(out, "private-") {
			t.Fatal("credential disclosure")
		}
	}
	n, _, err := normalize([]string{"[::1]:17301"}, Options{Credentials: Credentials{AuthToken: "token-value"}})
	if err != nil || n.Credentials.Username != "token" || n.Credentials.Password != "token-value" {
		t.Fatal("token setup", err)
	}
}

// Each fake connection accepts the production negotiation, then delegates reads.
func fakeServer(t *testing.T, handler func(net.Conn, *bufio.Reader)) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var conns []net.Conn
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				r := bufio.NewReader(conn)
				for _, s := range []struct{ in, out string }{{"WIREVER\n", "WIREVER 1\n"}, {"CODECMETA ON\n", "OK codec-metadata\n"}} {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if line != s.in {
						t.Error("bad negotiation")
						return
					}
					if _, err = io.WriteString(conn, s.out); err != nil {
						return
					}
				}
				handler(conn, r)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		mu.Lock()
		for _, c := range conns {
			_ = c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return listener.Addr().String()
}
func TestConcurrentClientAndClose(t *testing.T) {
	peer := fakeServer(t, func(c net.Conn, r *bufio.Reader) {
		for {
			h, e := r.ReadString('\n')
			if e != nil {
				return
			}
			if h != "HEALTH\n" {
				t.Error("interleaved command")
				return
			}
			_, _ = io.WriteString(c, "OK node=0\n")
		}
	})
	c, err := Dial(context.Background(), []string{peer}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				s, e := c.Health(context.Background())
				if e != nil || s != "node=0" {
					t.Error(s, e)
					return
				}
			}
		}()
	}
	wg.Wait()
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := c.Close(); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if _, err = c.Health(context.Background()); !errors.Is(err, ErrClosed) || !errors.Is(err, ErrConnection) {
		t.Fatal(err)
	}
}
func TestCancellationAndQueuedRequest(t *testing.T) {
	entered := make(chan struct{}, 1)
	peer := fakeServer(t, func(c net.Conn, r *bufio.Reader) {
		if _, e := r.ReadString('\n'); e != nil {
			return
		}
		entered <- struct{}{}
		_, _ = r.ReadByte()
	})
	c, err := Dial(context.Background(), []string{peer}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := c.Health(ctx); done <- e }()
	<-entered
	waitCtx, end := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer end()
	if _, e := c.Health(waitCtx); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	cancel()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation blocked")
	}
}
func TestCanceledWriteIsIndeterminate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	peer := fakeServer(t, func(c net.Conn, r *bufio.Reader) {
		h, e := r.ReadString('\n')
		if e != nil {
			return
		}
		if h != "PUTR 1 1 0 raw\n" {
			t.Error(h)
			return
		}
		b := make([]byte, 2)
		if _, e = io.ReadFull(r, b); e != nil {
			return
		}
		cancel()
		_, _ = r.ReadByte()
	})
	c, err := Dial(context.Background(), []string{peer}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, err = c.Put(ctx, "r", []byte("x"))
	if !errors.Is(err, ErrIndeterminateWrite) || !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestSecureFrameTamperingAndPartialReads(t *testing.T) {
	key := derive("fixture")
	cipher, err := encrypt(key, []byte("VAL 0 3 raw\nx\x00y"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tamper := range []bool{false, true} {
		t.Run(fmt.Sprint(tamper), func(t *testing.T) {
			b := append([]byte(nil), cipher...)
			if tamper {
				b[len(b)-1] ^= 1
			}
			wireText := append([]byte(fmt.Sprintf("SEC %d\n", len(b))), b...)
			w := &wire{reader: bufio.NewReader(strings.NewReader(string(wireText))), key: key}
			p, e := w.header(100)
			if tamper {
				if !errors.Is(e, ErrProtocol) {
					t.Fatal(e)
				}
				return
			}
			if e != nil || strings.Join(p, " ") != "VAL 0 3 raw" {
				t.Fatal(p, e)
			}
			got, e := w.bytes(3, 100)
			if e != nil || string(got) != "x\x00y" {
				t.Fatal(got, e)
			}
		})
	}
}
func TestNoWriteAfterCanceledContext(t *testing.T) {
	peer := fakeServer(t, func(c net.Conn, r *bufio.Reader) {
		if _, e := r.ReadByte(); e == nil {
			t.Error("canceled request sent")
		}
	})
	c, err := Dial(context.Background(), []string{peer}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = c.Put(ctx, "r", []byte("x")); !errors.Is(err, context.Canceled) || errors.Is(err, ErrIndeterminateWrite) {
		t.Fatal(err)
	}
}
func FuzzParseID(f *testing.F) {
	f.Add("18446744073709551615:1:7:1.25:60:0.5")
	f.Add("1:1:1:NaN:1:0")
	f.Fuzz(func(t *testing.T, s string) {
		id, e := ParseID(s)
		if e == nil {
			again, e := ParseID(id.String())
			if e != nil || again != id {
				t.Fatal("identity roundtrip")
			}
		}
	})
}
func FuzzHeader(f *testing.F) {
	f.Add("VAL 0 3 raw\n")
	f.Add("ERR hidden\n")
	f.Fuzz(func(t *testing.T, s string) { _, _ = line(bufio.NewReader(strings.NewReader(s)).ReadByte) })
}
