//go:build cgo && kouten_embedded

package embedded

/*
#cgo LDFLAGS: -lkoutendb
#include <stdlib.h>
#include "koutendb.h"
*/
import "C"

import (
	"encoding/json"
	"errors"
	"math"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	kouten "github.com/puffball1567/koutendb-go"
)

const ABIVersion = 2
const maxBytes = 64 * 1024 * 1024

var runtimeOnce sync.Once
var ErrABI = errors.New("koutendb: C ABI operation failed")
var ErrNotFound = errors.New("koutendb: record not found")

// ID is the C ABI identity, not the six-field TCP identity.
type ID struct {
	Parent     uint64
	Epoch, Seq uint32
	TWrite     float64
}

func (id ID) valid() bool { return !math.IsNaN(id.TWrite) && !math.IsInf(id.TWrite, 0) }
func toC(id ID) C.kouten_id {
	return C.kouten_id{parent: C.uint64_t(id.Parent), epoch: C.uint32_t(id.Epoch), seq: C.uint32_t(id.Seq), t_write: C.double(id.TWrite)}
}
func fromC(id C.kouten_id) ID {
	return ID{uint64(id.parent), uint32(id.epoch), uint32(id.seq), float64(id.t_write)}
}
func text(s string) bool { return len(s) < 1024*1024 && !strings.ContainsRune(s, 0) }
func bytesPtr(b []byte) unsafe.Pointer {
	if len(b) == 0 {
		return nil
	}
	return unsafe.Pointer(&b[0])
}
func floatPtr(v []float32) *C.float {
	if len(v) == 0 {
		return nil
	}
	return (*C.float)(unsafe.Pointer(&v[0]))
}
func codec(c kouten.Codec) (C.int, bool) {
	switch c {
	case kouten.Raw:
		return 0, true
	case kouten.JSON:
		return 1, true
	case kouten.NIF:
		return 2, true
	case kouten.BIF:
		return 3, true
	}
	return 0, false
}
func lastError() error {
	// Copy on the same OS thread before another ABI call; never expose its text.
	_ = C.GoString(C.kouten_last_error())
	return ErrABI
}
func buffer(p unsafe.Pointer, n C.size_t) ([]byte, error) {
	if p == nil {
		return nil, lastError()
	}
	defer C.kouten_free(p)
	if uint64(n) > maxBytes {
		return nil, ErrABI
	}
	return C.GoBytes(p, C.int(n)), nil
}

type result struct {
	value any
	err   error
}
type job struct {
	fn   func(unsafe.Pointer) (any, error)
	done chan result
}

// DB must not be copied. Methods may be called concurrently; Close is serialized.
type DB struct {
	mu     sync.Mutex
	jobs   chan job
	closed bool
}
type OpenOptions struct {
	Nodes                        int
	Directory                    string
	StrongDurability, DiskBacked bool
}

func Open(nodes int) (*DB, error) { return OpenWithOptions(OpenOptions{Nodes: nodes}) }
func OpenDir(nodes int, directory string) (*DB, error) {
	if directory == "" {
		return nil, kouten.ErrInvalidInput
	}
	return OpenWithOptions(OpenOptions{Nodes: nodes, Directory: directory})
}
func OpenWithOptions(o OpenOptions) (*DB, error) {
	if o.Nodes < 1 || o.Nodes > 65535 || !text(o.Directory) || o.Directory == "" && (o.StrongDurability || o.DiskBacked) {
		return nil, kouten.ErrInvalidInput
	}
	db := &DB{jobs: make(chan job)}
	ready := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		runtimeOnce.Do(func() { C.kouten_init() })
		if int(C.kouten_abi_version()) != ABIVersion {
			ready <- kouten.ErrVersionMismatch
			return
		}
		var raw unsafe.Pointer
		if o.Directory == "" {
			raw = C.kouten_open(C.int(o.Nodes))
		} else {
			dir := C.CString(o.Directory)
			raw = C.kouten_open_dir_options(C.int(o.Nodes), dir, flag(o.StrongDurability), flag(o.DiskBacked))
			C.free(unsafe.Pointer(dir))
		}
		if raw == nil {
			ready <- lastError()
			return
		}
		ready <- nil
		for j := range db.jobs {
			if j.fn == nil {
				C.kouten_close(raw)
				j.done <- result{}
				return
			}
			v, err := j.fn(raw)
			j.done <- result{v, err}
		}
	}()
	if err := <-ready; err != nil {
		return nil, err
	}
	return db, nil
}
func flag(b bool) C.int {
	if b {
		return 1
	}
	return 0
}
func (db *DB) call(fn func(unsafe.Pointer) (any, error)) (any, error) {
	if db == nil || db.jobs == nil {
		return nil, kouten.ErrClosed
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return nil, kouten.ErrClosed
	}
	done := make(chan result, 1)
	db.jobs <- job{fn, done}
	r := <-done
	return r.value, r.err
}
func (db *DB) Close() error {
	if db == nil || db.jobs == nil {
		return nil
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return nil
	}
	done := make(chan result, 1)
	db.jobs <- job{done: done}
	<-done
	db.closed = true
	return nil
}
func (db *DB) Put(ring string, data []byte) (ID, error) { return db.PutCodec(ring, data, kouten.Raw) }
func (db *DB) PutJSON(ring string, value any) (ID, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return ID{}, kouten.ErrInvalidInput
	}
	return db.PutCodec(ring, b, kouten.JSON)
}
func (db *DB) PutCodec(ring string, data []byte, encoding kouten.Codec) (ID, error) {
	return db.PutVec(ring, data, encoding, nil)
}
func (db *DB) PutVec(ring string, data []byte, encoding kouten.Codec, vector []float32) (ID, error) {
	c, ok := codec(encoding)
	if !ok || ring == "" || !text(ring) || len(data) > maxBytes || len(vector) > 1000000 {
		return ID{}, kouten.ErrInvalidInput
	}
	for _, v := range vector {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return ID{}, kouten.ErrInvalidInput
		}
	}
	v, err := db.call(func(raw unsafe.Pointer) (any, error) {
		r := C.CString(ring)
		defer C.free(unsafe.Pointer(r))
		var out C.kouten_id
		if C.kouten_put_vec_codec(raw, r, bytesPtr(data), C.size_t(len(data)), c, floatPtr(vector), C.size_t(len(vector)), &out) != C.KOUTEN_OK {
			return nil, lastError()
		}
		return fromC(out), nil
	})
	if err != nil {
		return ID{}, err
	}
	return v.(ID), nil
}
func (db *DB) Get(id ID) (*kouten.Payload, error) {
	if !id.valid() {
		return nil, kouten.ErrInvalidInput
	}
	v, err := db.call(func(raw unsafe.Pointer) (any, error) {
		switch C.kouten_exists(raw, toC(id)) {
		case 0:
			return (*kouten.Payload)(nil), nil
		case 1:
		default:
			return nil, lastError()
		}
		var n C.size_t
		var c C.int
		p := C.kouten_get_codec(raw, toC(id), &n, &c)
		b, err := buffer(p, n)
		if err != nil {
			return nil, err
		}
		if c < 0 || c > 3 {
			return nil, ErrABI
		}
		return &kouten.Payload{Data: b, Codec: []kouten.Codec{kouten.Raw, kouten.JSON, kouten.NIF, kouten.BIF}[int(c)]}, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*kouten.Payload), nil
}
func (db *DB) GetJSON(id ID, dst any) (bool, error) {
	p, err := db.Get(id)
	if err != nil || p == nil {
		return false, err
	}
	if p.Codec != kouten.JSON {
		return true, kouten.ErrProtocol
	}
	if json.Unmarshal(p.Data, dst) != nil {
		return true, kouten.ErrInvalidInput
	}
	return true, nil
}
func (db *DB) Query(id ID, selection string) ([]byte, error) {
	if !id.valid() || !text(selection) {
		return nil, kouten.ErrInvalidInput
	}
	v, err := db.call(func(raw unsafe.Pointer) (any, error) {
		if err := requireRecord(raw, id); err != nil {
			return nil, err
		}
		s := C.CString(selection)
		defer C.free(unsafe.Pointer(s))
		var n C.size_t
		p := C.kouten_query(raw, toC(id), s, &n)
		return buffer(p, n)
	})
	if err != nil {
		return nil, err
	}
	return v.([]byte), nil
}
func (db *DB) Update(id ID, data []byte, encoding kouten.Codec) error {
	c, ok := codec(encoding)
	if !ok || !id.valid() || len(data) > maxBytes {
		return kouten.ErrInvalidInput
	}
	_, err := db.call(func(raw unsafe.Pointer) (any, error) {
		if err := requireRecord(raw, id); err != nil {
			return nil, err
		}
		if C.kouten_update_codec(raw, toC(id), bytesPtr(data), C.size_t(len(data)), c) != C.KOUTEN_OK {
			return nil, lastError()
		}
		return nil, nil
	})
	return err
}
func (db *DB) Remove(id ID) error {
	if !id.valid() {
		return kouten.ErrInvalidInput
	}
	_, err := db.call(func(raw unsafe.Pointer) (any, error) {
		if err := requireRecord(raw, id); err != nil {
			return nil, err
		}
		if C.kouten_remove(raw, toC(id)) != C.KOUTEN_OK {
			return nil, lastError()
		}
		return nil, nil
	})
	return err
}

func requireRecord(raw unsafe.Pointer, id ID) error {
	switch C.kouten_exists(raw, toC(id)) {
	case 0:
		return ErrNotFound
	case 1:
		return nil
	default:
		return lastError()
	}
}

type RingOptions struct {
	Filter          json.RawMessage
	Selection       string
	Limit           int
	Cursor          string
	Pagination      bool
	Page, PageLimit int
	Sort            string
	Descending      bool
}

// ReadRing returns the core page-shaped JSON, retaining codec/encoding metadata.
func (db *DB) ReadRing(ring string, o RingOptions) (json.RawMessage, error) {
	if ring == "" || o.Limit < 0 || o.Limit > math.MaxInt32 || o.Page < 0 || o.Page > math.MaxInt32 || o.PageLimit < 0 || o.PageLimit > math.MaxInt32 {
		return nil, kouten.ErrInvalidInput
	}
	if o.Page == 0 {
		o.Page = 1
	}
	if o.PageLimit == 0 {
		o.PageLimit = 20
	}
	for _, s := range []string{ring, string(o.Filter), o.Selection, o.Cursor, o.Sort} {
		if !text(s) {
			return nil, kouten.ErrInvalidInput
		}
	}
	if len(o.Filter) > 0 {
		var obj map[string]any
		if json.Unmarshal(o.Filter, &obj) != nil || obj == nil {
			return nil, kouten.ErrInvalidInput
		}
	}
	v, err := db.call(func(raw unsafe.Pointer) (any, error) {
		fields := []string{ring, string(o.Filter), o.Selection, o.Cursor, o.Sort}
		c := make([]*C.char, len(fields))
		for i, s := range fields {
			c[i] = C.CString(s)
			defer C.free(unsafe.Pointer(c[i]))
		}
		var n C.size_t
		p := C.kouten_read_ring_json(raw, c[0], c[1], c[2], C.int(o.Limit), c[3], flag(o.Pagination), C.int(o.Page), C.int(o.PageLimit), c[4], flag(o.Descending), &n)
		return buffer(p, n)
	})
	if err != nil {
		return nil, err
	}
	b := v.([]byte)
	if !json.Valid(b) {
		return nil, ErrABI
	}
	return json.RawMessage(b), nil
}
