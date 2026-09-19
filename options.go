package koutendb

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const maxFrame = 64 * 1024 * 1024
const maxHeader = 8192

type Credentials struct{ Username, Password, AuthToken, SecretKey string }

func (Credentials) String() string               { return "Credentials([REDACTED])" }
func (c Credentials) GoString() string           { return c.String() }
func (Credentials) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }

type TLSOptions struct {
	CAFile     string
	ServerName string
	// InsecureSkipVerify disables chain and hostname checks. Development only.
	InsecureSkipVerify bool
}

// Options is copied at Dial. Zero durations/limits select defaults.
type Options struct {
	ConnectTimeout, ReadTimeout, WriteTimeout time.Duration
	MaxFrameBytes                             int
	MaxRedirects                              int
	DisableReadRetry                          bool
	Credentials                               Credentials
	Galaxy                                    string
	TLS                                       *TLSOptions
}

func (Options) String() string               { return "Options([REDACTED])" }
func (o Options) GoString() string           { return o.String() }
func (Options) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }

func normalize(peers []string, o Options) (Options, *tls.Config, error) {
	if len(peers) == 0 || len(peers) > 64 {
		return o, nil, ErrInvalidInput
	}
	for _, p := range peers {
		host, port, err := net.SplitHostPort(p)
		n, e := strconv.Atoi(port)
		if err != nil || e != nil || n < 1 || n > 65535 || host == "" || strings.ContainsAny(host, "/\\ \r\n\t\x00") {
			return o, nil, ErrInvalidInput
		}
	}
	for _, d := range []*time.Duration{&o.ConnectTimeout, &o.ReadTimeout, &o.WriteTimeout} {
		if *d == 0 {
			*d = 5 * time.Second
		}
		if *d < 0 || *d > time.Hour {
			return o, nil, ErrInvalidInput
		}
	}
	if o.MaxFrameBytes == 0 {
		o.MaxFrameBytes = maxFrame
	}
	if o.MaxRedirects == 0 {
		o.MaxRedirects = 8
	}
	if o.MaxFrameBytes < 1 || o.MaxFrameBytes > maxFrame || o.MaxRedirects < 0 || o.MaxRedirects > 32 {
		return o, nil, ErrInvalidInput
	}
	c := &o.Credentials
	if c.Username == "" && c.AuthToken != "" {
		c.Username, c.Password = "token", c.AuthToken
	}
	for _, field := range []string{c.Username, c.Password, o.Galaxy} {
		if len(field) > 1024 {
			return o, nil, ErrInvalidInput
		}
		for _, b := range []byte(field) {
			if b <= 32 || b == 127 {
				return o, nil, ErrInvalidInput
			}
		}
	}
	if len(c.SecretKey) > 4096 || c.Username == "" && (c.Password != "" || c.SecretKey != "") {
		return o, nil, ErrInvalidInput
	}
	if o.TLS == nil {
		return o, nil, nil
	}
	t := *o.TLS
	o.TLS = &t
	config := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: t.ServerName, InsecureSkipVerify: t.InsecureSkipVerify} // Explicit development opt-in.
	if t.CAFile != "" {
		pem, err := os.ReadFile(t.CAFile)
		if err != nil {
			return o, nil, ErrInvalidInput
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return o, nil, ErrInvalidInput
		}
		config.RootCAs = pool
	}
	return o, config, nil
}

func (c *Client) String() string   { return "KoutenDB TCP client" }
func (c *Client) GoString() string { return c.String() }

var _ fmt.Stringer = (*Client)(nil)
