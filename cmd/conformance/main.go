// Command conformance adapts JSONL requests to the shared core test harness.
package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	db "github.com/puffball1567/koutendb-go"
)

type options struct {
	Timeout, ReadTimeout, WriteTimeout               float64
	MaxFrameBytes, MaxRedirects                      int
	RetryReads                                       *bool
	Username, Password, AuthToken, SecretKey, Galaxy string
	TLS                                              bool
	TLSCaFile, TLSServerName                         string
	TLSInsecureSkipVerify                            bool
}
type request struct {
	Op, Ring, ID, Payload, Codec, Selection string
	Peers                                   []string
	Value                                   json.RawMessage
	Options                                 options
	Timeout, ReadTimeout, WriteTimeout      float64
}

func kind(err error) string {
	for _, pair := range []struct {
		e    error
		name string
	}{
		{db.ErrIndeterminateWrite, "IndeterminateWriteException"},
		{db.ErrAuthentication, "AuthenticationException"},
		{db.ErrProtocol, "ProtocolException"},
		{db.ErrVersionMismatch, "VersionMismatchException"},
		{db.ErrTimeout, "ConnectionTimeoutException"},
		{db.ErrConnection, "ConnectionException"},
		{db.ErrServer, "ServerException"},
	} {
		if errors.Is(err, pair.e) {
			return pair.name
		}
	}
	return "InvalidInputException"
}
func duration(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }
func run(c **db.Client, r request) (any, error) {
	ctx := context.Background()
	if r.Op == "connect" {
		if *c != nil {
			_ = (*c).Close()
			*c = nil
		}
		o := r.Options
		if r.Timeout != 0 {
			o.Timeout = r.Timeout
		}
		if r.ReadTimeout != 0 {
			o.ReadTimeout = r.ReadTimeout
		}
		if r.WriteTimeout != 0 {
			o.WriteTimeout = r.WriteTimeout
		}
		if o.Timeout == 0 {
			o.Timeout = 1
		}
		if o.ReadTimeout == 0 {
			o.ReadTimeout = 1
		}
		if o.WriteTimeout == 0 {
			o.WriteTimeout = 1
		}
		settings := db.Options{ConnectTimeout: duration(o.Timeout), ReadTimeout: duration(o.ReadTimeout), WriteTimeout: duration(o.WriteTimeout), MaxFrameBytes: o.MaxFrameBytes, MaxRedirects: o.MaxRedirects, Galaxy: o.Galaxy, Credentials: db.Credentials{Username: o.Username, Password: o.Password, AuthToken: o.AuthToken, SecretKey: o.SecretKey}, DisableReadRetry: o.RetryReads != nil && !*o.RetryReads}
		if o.TLS {
			settings.TLS = &db.TLSOptions{CAFile: o.TLSCaFile, ServerName: o.TLSServerName, InsecureSkipVerify: o.TLSInsecureSkipVerify}
		}
		var err error
		*c, err = db.Dial(ctx, r.Peers, settings)
		return "connected", err
	}
	if *c == nil {
		return nil, db.ErrClosed
	}
	switch r.Op {
	case "close":
		return "closed", (*c).Close()
	case "debug":
		return fmt.Sprintf("%#v", *c), nil
	case "health":
		return (*c).Health(ctx)
	case "put":
		b, err := base64.StdEncoding.DecodeString(r.Payload)
		if err != nil {
			return nil, db.ErrInvalidInput
		}
		codec := db.Codec(r.Codec)
		if codec == "" {
			codec = db.Raw
		}
		id, err := (*c).PutCodec(ctx, r.Ring, b, codec)
		return id.String(), err
	case "putJson":
		id, err := (*c).PutJSON(ctx, r.Ring, r.Value)
		return id.String(), err
	case "get", "getJson", "query":
		id, err := db.ParseID(r.ID)
		if err != nil {
			return nil, err
		}
		if r.Op == "get" {
			p, err := (*c).Get(ctx, id)
			if p == nil || err != nil {
				return nil, err
			}
			return map[string]any{"payload": base64.StdEncoding.EncodeToString(p.Data), "codec": p.Codec}, nil
		}
		var v json.RawMessage
		var found bool
		if r.Op == "getJson" {
			found, err = (*c).GetJSON(ctx, id, &v)
		} else {
			found, err = (*c).QueryJSON(ctx, id, r.Selection, &v)
		}
		if !found {
			return nil, err
		}
		return v, err
	}
	return nil, db.ErrInvalidInput
}
func main() {
	var client *db.Client
	defer func() {
		if client != nil {
			_ = client.Close()
		}
	}()
	s := bufio.NewScanner(os.Stdin)
	s.Buffer(make([]byte, 4096), 128*1024*1024)
	out := json.NewEncoder(os.Stdout)
	for s.Scan() {
		var r request
		err := json.Unmarshal(s.Bytes(), &r)
		var result any
		if err == nil {
			result, err = run(&client, r)
		} else {
			err = db.ErrInvalidInput
		}
		response := map[string]any{"ok": err == nil}
		if err == nil {
			response["result"] = result
		} else {
			response["error"] = kind(err)
			response["message"] = err.Error()
		}
		if out.Encode(response) != nil {
			os.Exit(1)
		}
	}
	if s.Err() != nil {
		os.Exit(1)
	}
}
