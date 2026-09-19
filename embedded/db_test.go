//go:build cgo && kouten_embedded

package embedded

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	kouten "github.com/puffball1567/koutendb-go"
)

func TestCRUDCodecMatrix(t *testing.T) {
	db, e := Open(8)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	for _, c := range []kouten.Codec{kouten.Raw, kouten.JSON, kouten.NIF, kouten.BIF} {
		for _, b := range [][]byte{{}, []byte("x\x00y"), []byte("日本語"), make([]byte, 1024*1024)} {
			t.Run(fmt.Sprintf("%s/%d", c, len(b)), func(t *testing.T) {
				id, e := db.PutCodec("docs/go", b, c)
				if e != nil {
					t.Fatal(e)
				}
				p, e := db.Get(id)
				if e != nil || p == nil || p.Codec != c || string(p.Data) != string(b) {
					t.Fatal("roundtrip", e)
				}
				if e = db.Update(id, []byte("updated"), kouten.Raw); e != nil {
					t.Fatal(e)
				}
				p, e = db.Get(id)
				if e != nil || p == nil || string(p.Data) != "updated" {
					t.Fatal("update", e)
				}
				if e = db.Remove(id); e != nil {
					t.Fatal(e)
				}
				p, e = db.Get(id)
				if e != nil || p != nil {
					t.Fatal("delete", p, e)
				}
			})
		}
	}
}
func TestPersistentReopenAndRingRead(t *testing.T) {
	dir := t.TempDir()
	db, e := OpenWithOptions(OpenOptions{Nodes: 8, Directory: dir, StrongDurability: true, DiskBacked: true})
	if e != nil {
		t.Fatal(e)
	}
	id, e := db.PutJSON("users/123/profile", map[string]any{"name": "Ada", "active": true})
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	db, e = OpenDir(8, dir)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var user struct{ Name string }
	found, e := db.GetJSON(id, &user)
	if e != nil || !found || user.Name != "Ada" {
		t.Fatal(user, found, e)
	}
	b, e := db.Query(id, "{ name }")
	if e != nil || !json.Valid(b) {
		t.Fatal(string(b), e)
	}
	page, e := db.ReadRing("users/123/profile", RingOptions{Filter: json.RawMessage(`{"active":true}`), Selection: "{ name }", Limit: 10})
	if e != nil {
		t.Fatal(e)
	}
	var result struct{ Items []json.RawMessage }
	if e = json.Unmarshal(page, &result); e != nil || len(result.Items) != 1 {
		t.Fatalf("page %s %v", page, e)
	}
}
func TestConcurrentCallsAndClose(t *testing.T) {
	db, e := Open(8)
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				id, e := db.Put("docs", []byte("go"))
				if e != nil {
					t.Error(e)
					return
				}
				p, e := db.Get(id)
				if e != nil || p == nil || string(p.Data) != "go" {
					t.Error("get", e)
					return
				}
			}
		}()
	}
	wg.Wait()
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = db.Close()
			_, e := db.Put("docs", []byte("x"))
			if !errors.Is(e, kouten.ErrClosed) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
}
func TestInvalidInputsAndVector(t *testing.T) {
	for _, db := range []*DB{nil, {}} {
		if _, e := db.Put("docs", nil); !errors.Is(e, kouten.ErrClosed) {
			t.Fatal(e)
		}
		if e := db.Close(); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := Open(0); !errors.Is(e, kouten.ErrInvalidInput) {
		t.Fatal(e)
	}
	db, e := Open(8)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	for _, r := range []string{"", "x\x00y"} {
		if _, e = db.Put(r, nil); !errors.Is(e, kouten.ErrInvalidInput) {
			t.Fatal(e)
		}
	}
	if _, e = db.ReadRing("docs", RingOptions{Limit: -1}); !errors.Is(e, kouten.ErrInvalidInput) {
		t.Fatal(e)
	}
	if _, e = db.ReadRing("docs", RingOptions{Filter: json.RawMessage(`[]`)}); !errors.Is(e, kouten.ErrInvalidInput) {
		t.Fatal(e)
	}
	id, e := db.PutVec("docs", []byte("vector"), kouten.Raw, []float32{1, 0})
	if e != nil {
		t.Fatal(e)
	}
	p, e := db.Get(id)
	if e != nil || p == nil || string(p.Data) != "vector" {
		t.Fatal(e)
	}
	if e = db.Remove(id); e != nil {
		t.Fatal(e)
	}
	if e = db.Remove(id); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if e = db.Update(id, nil, kouten.Raw); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if _, e = db.Query(id, "{ name }"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
}
