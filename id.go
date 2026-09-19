package koutendb

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ID preserves all six server-supplied fields. Never calculate placement locally.
type ID struct {
	Parent uint64
	Epoch  uint32
	Seq    uint32
	TWrite float64
	Period float64
	Head   float64
}

func (id ID) String() string {
	return fmt.Sprintf("%d:%d:%d:%s:%s:%s", id.Parent, id.Epoch, id.Seq,
		strconv.FormatFloat(id.TWrite, 'g', -1, 64), strconv.FormatFloat(id.Period, 'g', -1, 64), strconv.FormatFloat(id.Head, 'g', -1, 64))
}
func (id ID) valid() bool {
	return finite(id.TWrite) && finite(id.Period) && id.Period > 0 && finite(id.Head)
}
func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }
func uintField(s string, bits int) (uint64, error) {
	if s == "" || len(s) > 1 && s[0] == '0' {
		return 0, ErrProtocol
	}
	for _, b := range []byte(s) {
		if b < '0' || b > '9' {
			return 0, ErrProtocol
		}
	}
	u, err := strconv.ParseUint(s, 10, bits)
	if err != nil {
		return 0, ErrProtocol
	}
	return u, nil
}
func bounded(s string, max int) (int, error) {
	u, err := uintField(s, 64)
	if err != nil || u > uint64(max) {
		return 0, ErrProtocol
	}
	return int(u), nil
}
func parseFields(p []string) (ID, error) {
	if len(p) != 6 {
		return ID{}, ErrProtocol
	}
	a, e1 := uintField(p[0], 64)
	b, e2 := uintField(p[1], 32)
	c, e3 := uintField(p[2], 32)
	t, e4 := strconv.ParseFloat(p[3], 64)
	period, e5 := strconv.ParseFloat(p[4], 64)
	head, e6 := strconv.ParseFloat(p[5], 64)
	id := ID{a, uint32(b), uint32(c), t, period, head}
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil || e6 != nil || !id.valid() {
		return ID{}, ErrProtocol
	}
	return id, nil
}

// ParseID parses the complete colon-separated wire identity.
func ParseID(s string) (ID, error) {
	id, err := parseFields(strings.Split(s, ":"))
	if err != nil {
		return ID{}, ErrInvalidInput
	}
	return id, nil
}
func (id ID) MarshalText() ([]byte, error) {
	if !id.valid() {
		return nil, ErrInvalidInput
	}
	return []byte(id.String()), nil
}
func (id *ID) UnmarshalText(b []byte) error {
	n, err := ParseID(string(b))
	if err == nil {
		*id = n
	}
	return err
}

type Codec string

const (
	Raw  Codec = "raw"
	JSON Codec = "json"
	NIF  Codec = "nif"
	BIF  Codec = "bif"
)

func (c Codec) Valid() bool { return c == Raw || c == JSON || c == NIF || c == BIF }

// Payload distinguishes an empty value from an absent record (a nil *Payload).
type Payload struct {
	Data  []byte
	Codec Codec
}
