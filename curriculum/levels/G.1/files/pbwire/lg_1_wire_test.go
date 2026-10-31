package pbwire_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"burrow/pbwire"
	"burrow/store"
	"burrow/watch"
)

// h turns "0a 01 61" into []byte{0x0a, 0x01, 0x61}.
func h(s string) []byte {
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		panic(err)
	}
	return b
}

func entryEqual(a, b store.Entry) bool {
	return a.Key == b.Key && bytes.Equal(a.Value, b.Value) &&
		a.Version == b.Version && a.ExpiresAt.Equal(b.ExpiresAt)
}

func eventEqual(a, b watch.Event) bool {
	return a.ID == b.ID && a.Type == b.Type && entryEqual(a.Entry, b.Entry)
}

func showEntry(e store.Entry) string {
	exp := "none"
	if !e.ExpiresAt.IsZero() {
		exp = fmt.Sprint(e.ExpiresAt.UnixNano())
	}
	return fmt.Sprintf("{Key:%q Value:%q Version:%d ExpiresAt:%s}",
		e.Key, e.Value, e.Version, exp)
}

func showEvent(ev watch.Event) string {
	return fmt.Sprintf("{ID:%d Type:%q Entry:%s}", ev.ID, ev.Type, showEntry(ev.Entry))
}

// 2026-01-01T00:00:00Z
const jan2026 = 1767225600000000000

// Every golden encoding below is what google.golang.org/protobuf's
// proto.Marshal produces for the same burrow.v1 message.

func TestLG_1_Varint(t *testing.T) {
	cases := []struct {
		v    uint64
		want string
	}{
		{0, "00"},
		{1, "01"},
		{127, "7f"},
		{128, "80 01"},
		{300, "ac 02"},
		{16383, "ff 7f"},
		{16384, "80 80 01"},
		{1<<32 - 1, "ff ff ff ff 0f"},
		{1 << 63, "80 80 80 80 80 80 80 80 80 01"},
		{math.MaxUint64, "ff ff ff ff ff ff ff ff ff 01"},
	}
	for _, c := range cases {
		got := pbwire.AppendVarint(nil, c.v)
		if !bytes.Equal(got, h(c.want)) {
			t.Errorf("AppendVarint(%d) = % x, want %s (7 bits per byte, low bits first, "+
				"top bit set on every byte but the last)", c.v, got, c.want)
		}
		v, n, err := pbwire.ConsumeVarint(h(c.want + " 99 99"))
		if err != nil || v != c.v || n != len(h(c.want)) {
			t.Errorf("ConsumeVarint(%s followed by 2 more bytes) = (%d, %d, %v), want (%d, %d, nil): "+
				"stop at the first byte without the top bit and report how many bytes you used",
				c.want, v, n, err, c.v, len(h(c.want)))
		}
	}
	prefix := []byte("keep")
	out := pbwire.AppendVarint(prefix, 300)
	if string(out) != "keep\xac\x02" {
		t.Errorf("AppendVarint(\"keep\", 300) = %q: append to b, leaving what is there", out)
	}
	r := rand.New(rand.NewPCG(7, 8))
	for range 2000 {
		v := r.Uint64() >> r.UintN(64)
		b := pbwire.AppendVarint(nil, v)
		got, n, err := pbwire.ConsumeVarint(b)
		if err != nil || got != v || n != len(b) {
			t.Fatalf("round trip of %d via % x = (%d, %d, %v)", v, b, got, n, err)
		}
	}
}

func TestLG_1_VarintNonMinimal(t *testing.T) {
	// Padding with 0x80 bytes is legal, as long as it fits in 10 bytes.
	for _, c := range []struct {
		in   string
		want uint64
	}{
		{"80 00", 0},
		{"81 80 00", 1},
		{"80 80 80 80 80 80 80 80 80 00", 0},
	} {
		v, n, err := pbwire.ConsumeVarint(h(c.in))
		if err != nil || v != c.want || n != len(h(c.in)) {
			t.Errorf("ConsumeVarint(%s) = (%d, %d, %v), want (%d, %d, nil): "+
				"a padded varint is still valid (protobuf accepts it)", c.in, v, n, err, c.want, len(h(c.in)))
		}
	}
}

func TestLG_1_VarintErrors(t *testing.T) {
	cases := []struct {
		in   string
		want error
		why  string
	}{
		{"", pbwire.ErrTruncated, "no bytes at all"},
		{"80", pbwire.ErrTruncated, "the top bit promises another byte"},
		{"ff ff", pbwire.ErrTruncated, "the top bit promises another byte"},
		{"ff ff ff ff ff ff ff ff ff", pbwire.ErrTruncated, "nine bytes, all continued"},
		{"ff ff ff ff ff ff ff ff ff 02", pbwire.ErrOverflow, "the 10th byte can only hold the 64th bit (0 or 1)"},
		{"80 80 80 80 80 80 80 80 80 81", pbwire.ErrOverflow, "a 10th byte above 1 is more than 64 bits"},
		{"ff ff ff ff ff ff ff ff ff ff", pbwire.ErrOverflow, "a varint never needs an 11th byte"},
		{"80 80 80 80 80 80 80 80 80 80 01", pbwire.ErrOverflow, "11 bytes is overlong even if the value is small"},
	}
	for _, c := range cases {
		_, _, err := pbwire.ConsumeVarint(h(c.in))
		if !errors.Is(err, c.want) {
			t.Errorf("ConsumeVarint(%s): want %v (%s), got %v", c.in, c.want, c.why, err)
		}
	}
}

var entryGolden = []struct {
	name string
	e    store.Entry
	want string
}{
	{"zero entry", store.Entry{}, ""},
	{"key only", store.Entry{Key: "a"}, "0a 01 61"},
	{"key, value, version",
		store.Entry{Key: "user:1", Value: []byte("ann"), Version: 3},
		"0a 06 75 73 65 72 3a 31 12 03 61 6e 6e 18 03"},
	{"two-byte version", store.Entry{Key: "k", Version: 300}, "0a 01 6b 18 ac 02"},
	{"max version", store.Entry{Key: "k", Version: math.MaxUint64},
		"0a 01 6b 18 ff ff ff ff ff ff ff ff ff 01"},
	{"expiry", store.Entry{Key: "k", ExpiresAt: time.Unix(0, jan2026)},
		"0a 01 6b 20 80 80 e8 ef 9e ca 9c c3 18"},
	{"expiry in another zone",
		store.Entry{Key: "k", ExpiresAt: time.Unix(0, jan2026).In(time.FixedZone("AEST", 10*3600))},
		"0a 01 6b 20 80 80 e8 ef 9e ca 9c c3 18"},
	{"negative int64 is ten bytes", store.Entry{Key: "k", ExpiresAt: time.Unix(0, -1)},
		"0a 01 6b 20 ff ff ff ff ff ff ff ff ff 01"},
	{"200-byte value", store.Entry{Key: "k", Value: bytes.Repeat([]byte("x"), 200)},
		"0a 01 6b 12 c8 01" + strings.Repeat(" 78", 200)},
	{"value only", store.Entry{Value: []byte{0, 0xff}}, "12 02 00 ff"},
	{"every field",
		store.Entry{Key: "cfg/x", Value: []byte{1, 2}, Version: 1 << 35, ExpiresAt: time.Unix(0, 1)},
		"0a 05 63 66 67 2f 78 12 02 01 02 18 80 80 80 80 80 01 20 01"},
}

func TestLG_1_EntryGolden(t *testing.T) {
	for _, c := range entryGolden {
		got := pbwire.MarshalEntry(c.e)
		if !bytes.Equal(got, h(c.want)) {
			t.Errorf("%s: MarshalEntry(%s)\n got % x\nwant %s", c.name, showEntry(c.e), got, c.want)
		}
		back, err := pbwire.UnmarshalEntry(h(c.want))
		if err != nil || !entryEqual(back, c.e) {
			t.Errorf("%s: UnmarshalEntry(%s) = (%s, %v), want %s", c.name, c.want, showEntry(back), err, showEntry(c.e))
		}
	}
}

func TestLG_1_ZeroOmitted(t *testing.T) {
	cases := []struct {
		e    store.Entry
		want string
	}{
		{store.Entry{}, ""},
		{store.Entry{Key: "k", Value: []byte{}}, "0a 01 6b"},
		{store.Entry{Key: "k", Version: 0}, "0a 01 6b"},
		{store.Entry{Key: "k", ExpiresAt: time.Time{}}, "0a 01 6b"},
	}
	for _, c := range cases {
		if got := pbwire.MarshalEntry(c.e); !bytes.Equal(got, h(c.want)) {
			t.Errorf("MarshalEntry(%s) = % x, want %q: proto3 writes nothing for a zero value "+
				"(empty string or bytes, 0, no expiry)", showEntry(c.e), got, c.want)
		}
	}
	if got := pbwire.MarshalEvent(watch.Event{}); len(got) != 0 {
		t.Errorf("MarshalEvent(zero Event) = % x, want no bytes at all", got)
	}
	// Zeros written out anyway are still fine to read.
	e, err := pbwire.UnmarshalEntry(h("0a 00 12 00 18 00 20 00"))
	if err != nil || !entryEqual(e, store.Entry{}) || !e.ExpiresAt.IsZero() {
		t.Errorf("explicit zeros: got (%s, %v), want the zero Entry with a zero ExpiresAt "+
			"(expires_at_unix_nano 0 means no expiry)", showEntry(e), err)
	}
	if e, err := pbwire.UnmarshalEntry(nil); err != nil || !entryEqual(e, store.Entry{}) {
		t.Errorf("no bytes is a valid, empty Entry: got (%s, %v)", showEntry(e), err)
	}
}

// Unknown fields of every wire type, as a newer writer might send them.
var unknowns = []struct{ name, bytes string }{
	{"varint field 5", "28 96 01"},
	{"i64 field 6", "31 01 02 03 04 05 06 07 08"},
	{"len field 7", "3a 03 61 62 63"},
	{"empty len field 7", "3a 00"},
	{"i32 field 9", "4d 01 02 03 04"},
	{"group field 8", "43 08 01 12 01 7a 44"},
	{"group in a group", "43 53 28 01 54 31 00 00 00 00 00 00 00 00 44"},
	{"field 15 (one-byte tag limit)", "78 01"},
	{"field 16 (two-byte tag)", "80 01 01"},
	{"field 536870911 (the largest)", "f8 ff ff ff 0f 01"},
	{"a tag for field 3 inside a skipped len", "3a 02 18 07"},
}

func TestLG_1_UnknownFieldsSkipped(t *testing.T) {
	want := store.Entry{Key: "k", Version: 3}
	for _, u := range unknowns {
		for _, in := range []string{
			u.bytes + " 0a 01 6b 18 03",
			"0a 01 6b " + u.bytes + " 18 03",
			"0a 01 6b 18 03 " + u.bytes,
		} {
			e, err := pbwire.UnmarshalEntry(h(in))
			if err != nil || !entryEqual(e, want) {
				t.Errorf("%s: UnmarshalEntry(%s) = (%s, %v), want %s: skip a field you don't know "+
					"by its wire type", u.name, in, showEntry(e), err, showEntry(want))
			}
		}
		// The same field at the top of an Event and inside its Entry.
		in := "08 01 " + u.bytes + " 10 01 1a " + fmt.Sprintf("%02x", 5+len(h(u.bytes))) +
			" 0a 01 6b " + u.bytes + " 18 03"
		ev, err := pbwire.UnmarshalEvent(h(in))
		wantEv := watch.Event{ID: 1, Type: "put", Entry: want}
		if err != nil || !eventEqual(ev, wantEv) {
			t.Errorf("%s: UnmarshalEvent(%s) = (%s, %v), want %s: unknown fields can appear in the "+
				"nested Entry too", u.name, in, showEvent(ev), err, showEvent(wantEv))
		}
	}
}

func TestLG_1_Malformed(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want error
	}{
		{"tag with no value", "0a", pbwire.ErrTruncated},
		{"key longer than the input", "0a 05 61 62", pbwire.ErrTruncated},
		{"huge key length", "0a ff ff ff ff 0f 61", pbwire.ErrTruncated},
		{"version cut short", "0a 01 6b 18 ac", pbwire.ErrTruncated},
		{"truncated tag", "80", pbwire.ErrTruncated},
		{"unknown i64 cut short", "31 01 02 03", pbwire.ErrTruncated},
		{"unknown i32 cut short", "4d 01 02", pbwire.ErrTruncated},
		{"unknown len cut short", "3a 04 61", pbwire.ErrTruncated},
		{"group with no end", "43 08 01", pbwire.ErrTruncated},
		{"version over 64 bits", "18 ff ff ff ff ff ff ff ff ff 02", pbwire.ErrOverflow},
		{"11-byte version", "18 80 80 80 80 80 80 80 80 80 80 00", pbwire.ErrOverflow},
		{"length over 64 bits", "0a ff ff ff ff ff ff ff ff ff 7f", pbwire.ErrOverflow},
		{"wire type 6", "0e 00", pbwire.ErrWireType},
		{"wire type 7", "3f 00", pbwire.ErrWireType},
		{"field number 0", "00 01", pbwire.ErrWireType},
		{"end group with no start", "44", pbwire.ErrWireType},
		{"group ended by the wrong field", "43 08 01 4c", pbwire.ErrWireType},
		{"key sent as a varint", "08 01", pbwire.ErrWireType},
		{"value sent as a varint", "10 01", pbwire.ErrWireType},
		{"version sent as bytes", "1a 01 03", pbwire.ErrWireType},
		{"expiry sent as fixed64", "21 01 00 00 00 00 00 00 00", pbwire.ErrWireType},
	}
	for _, c := range cases {
		e, err := pbwire.UnmarshalEntry(h(c.in))
		if !errors.Is(err, c.want) {
			t.Errorf("%s: UnmarshalEntry(%s): want %v, got %v (%s)", c.name, c.in, c.want, err, showEntry(e))
		} else if !entryEqual(e, store.Entry{}) {
			t.Errorf("%s: on error UnmarshalEntry returns the zero Entry, got %s", c.name, showEntry(e))
		}
	}
	evCases := []struct {
		name string
		in   string
		want error
	}{
		{"id sent as bytes", "0a 00", pbwire.ErrWireType},
		{"type sent as fixed32", "15 01 00 00 00", pbwire.ErrWireType},
		{"entry sent as a varint", "18 01", pbwire.ErrWireType},
		{"entry longer than the input", "1a 05 0a 01 6b", pbwire.ErrTruncated},
		{"key runs past the end of its entry", "1a 02 0a 03 61 62 63", pbwire.ErrTruncated},
		{"bad field inside the entry", "1a 02 08 01", pbwire.ErrWireType},
		{"overflow inside the entry", "1a 0b 18 ff ff ff ff ff ff ff ff ff 02", pbwire.ErrOverflow},
		{"id cut short", "08 ff", pbwire.ErrTruncated},
	}
	for _, c := range evCases {
		ev, err := pbwire.UnmarshalEvent(h(c.in))
		if !errors.Is(err, c.want) {
			t.Errorf("%s: UnmarshalEvent(%s): want %v, got %v (%s)", c.name, c.in, c.want, err, showEvent(ev))
		}
	}
}

func TestLG_1_LastOneWins(t *testing.T) {
	e, err := pbwire.UnmarshalEntry(h("0a 01 61 18 01 0a 01 62 18 02"))
	if want := (store.Entry{Key: "b", Version: 2}); err != nil || !entryEqual(e, want) {
		t.Errorf("a field seen twice: the last one wins. got (%s, %v), want %s", showEntry(e), err, showEntry(want))
	}
	// Two encodings back to back decode as one merged message.
	a := store.Entry{Key: "a", Value: []byte("v"), Version: 9}
	b := store.Entry{Key: "b", ExpiresAt: time.Unix(0, jan2026)}
	in := append(pbwire.MarshalEntry(a), pbwire.MarshalEntry(b)...)
	e, err = pbwire.UnmarshalEntry(in)
	want := store.Entry{Key: "b", Value: []byte("v"), Version: 9, ExpiresAt: time.Unix(0, jan2026)}
	if err != nil || !entryEqual(e, want) {
		t.Errorf("Marshal(a)+Marshal(b) should decode as a merged with b: got (%s, %v), want %s",
			showEntry(e), err, showEntry(want))
	}
	// A nested message seen twice is merged, not replaced.
	ev, err := pbwire.UnmarshalEvent(h("08 01 1a 05 0a 01 6b 18 07 1a 02 20 01"))
	wantEv := watch.Event{ID: 1, Entry: store.Entry{Key: "k", Version: 7, ExpiresAt: time.Unix(0, 1)}}
	if err != nil || !eventEqual(ev, wantEv) {
		t.Errorf("an entry field seen twice merges into the first: got (%s, %v), want %s. "+
			"Decode the second one into the Entry you already have.", showEvent(ev), err, showEvent(wantEv))
	}
}

func TestLG_1_ValueIsCopied(t *testing.T) {
	in := h("0a 01 6b 12 03 61 62 63")
	e, err := pbwire.UnmarshalEntry(in)
	if err != nil {
		t.Fatal(err)
	}
	for i := range in {
		in[i] = 'X'
	}
	if e.Key != "k" || string(e.Value) != "abc" {
		t.Errorf("after overwriting the input, the decoded entry changed to %s: "+
			"copy the value out of b (bytes.Clone)", showEntry(e))
	}
}

var eventGolden = []struct {
	name string
	ev   watch.Event
	want string
}{
	{"zero event", watch.Event{}, ""},
	{"ready has no entry", watch.Event{ID: 7, Type: "ready"}, "08 07 10 04"},
	{"put",
		watch.Event{ID: 1, Type: "put", Entry: store.Entry{Key: "user:1", Value: []byte("ann"), Version: 1}},
		"08 01 10 01 1a 0f 0a 06 75 73 65 72 3a 31 12 03 61 6e 6e 18 01"},
	{"delete carries only the key",
		watch.Event{ID: 2, Type: "delete", Entry: store.Entry{Key: "k"}},
		"08 02 10 02 1a 03 0a 01 6b"},
	{"expire",
		watch.Event{ID: 300, Type: "expire",
			Entry: store.Entry{Key: "k", Version: 4, ExpiresAt: time.Unix(0, jan2026)}},
		"08 ac 02 10 03 1a 0f 0a 01 6b 18 04 20 80 80 e8 ef 9e ca 9c c3 18"},
	{"no type", watch.Event{ID: 5}, "08 05"},
}

func TestLG_1_EventGolden(t *testing.T) {
	for _, c := range eventGolden {
		got := pbwire.MarshalEvent(c.ev)
		if !bytes.Equal(got, h(c.want)) {
			t.Errorf("%s: MarshalEvent(%s)\n got % x\nwant %s", c.name, showEvent(c.ev), got, c.want)
		}
		back, err := pbwire.UnmarshalEvent(h(c.want))
		if err != nil || !eventEqual(back, c.ev) {
			t.Errorf("%s: UnmarshalEvent(%s) = (%s, %v), want %s", c.name, c.want, showEvent(back), err, showEvent(c.ev))
		}
	}
	// The entry field is written only when the entry has a key.
	ev := watch.Event{ID: 1, Type: "put", Entry: store.Entry{Value: []byte("x"), Version: 3}}
	if got := pbwire.MarshalEvent(ev); !bytes.Equal(got, h("08 01 10 01")) {
		t.Errorf("MarshalEvent(%s) = % x, want 08 01 10 01: no key, no entry field", showEvent(ev), got)
	}
	// An empty entry field still decodes, to the zero Entry.
	if ev, err := pbwire.UnmarshalEvent(h("08 01 1a 00")); err != nil || !eventEqual(ev, watch.Event{ID: 1}) {
		t.Errorf("UnmarshalEvent(08 01 1a 00) = (%s, %v), want ID 1 and a zero Entry", showEvent(ev), err)
	}
}

func TestLG_1_EventTypes(t *testing.T) {
	for _, c := range []struct {
		typ string
		num byte
	}{{"put", 1}, {"delete", 2}, {"expire", 3}, {"ready", 4}} {
		got := pbwire.MarshalEvent(watch.Event{Type: c.typ})
		if want := []byte{0x10, c.num}; !bytes.Equal(got, want) {
			t.Errorf("MarshalEvent(Type %q) = % x, want % x (EVENT_TYPE_%s = %d)",
				c.typ, got, want, strings.ToUpper(c.typ), c.num)
		}
		ev, err := pbwire.UnmarshalEvent([]byte{0x10, c.num})
		if err != nil || ev.Type != c.typ {
			t.Errorf("UnmarshalEvent(10 %02x) = (Type %q, %v), want Type %q", c.num, ev.Type, err, c.typ)
		}
	}
	for _, typ := range []string{"", "bogus", "PUT"} {
		if got := pbwire.MarshalEvent(watch.Event{Type: typ}); len(got) != 0 {
			t.Errorf("MarshalEvent(Type %q) = % x: a type with no enum value is UNSPECIFIED (0), "+
				"which proto3 omits", typ, got)
		}
	}
	for _, in := range []string{"10 00", "10 05", "10 7f"} {
		ev, err := pbwire.UnmarshalEvent(h(in))
		if err != nil || ev.Type != "" {
			t.Errorf("UnmarshalEvent(%s) = (Type %q, %v), want Type \"\" and no error: UNSPECIFIED "+
				"and enum numbers you don't know (yet) are not errors", in, ev.Type, err)
		}
	}
}

func TestLG_1_Expiry(t *testing.T) {
	for _, ns := range []int64{1, -1, jan2026, math.MaxInt64, math.MinInt64, 1 << 40} {
		in := store.Entry{Key: "k", ExpiresAt: time.Unix(0, ns)}
		out, err := pbwire.UnmarshalEntry(pbwire.MarshalEntry(in))
		if err != nil || !out.ExpiresAt.Equal(in.ExpiresAt) {
			t.Errorf("ExpiresAt at %d ns: came back as (%s, %v)", ns, showEntry(out), err)
		}
	}
	out, err := pbwire.UnmarshalEntry(pbwire.MarshalEntry(store.Entry{Key: "k"}))
	if err != nil || !out.ExpiresAt.IsZero() {
		t.Errorf("no expiry must come back as the zero time.Time (IsZero), got %v, %v", out.ExpiresAt, err)
	}
}

func randEntry(r *rand.Rand) store.Entry {
	var e store.Entry
	if r.IntN(4) > 0 {
		e.Key = fmt.Sprintf("k%d/%s", r.IntN(1000), strings.Repeat("x", r.IntN(300)))
	}
	if r.IntN(2) == 0 {
		e.Value = make([]byte, r.IntN(500))
		for i := range e.Value {
			e.Value[i] = byte(r.Uint32())
		}
	}
	if r.IntN(2) == 0 {
		e.Version = r.Uint64() >> r.UintN(64)
	}
	if r.IntN(2) == 0 {
		e.ExpiresAt = time.Unix(0, int64(r.Uint64()))
	}
	return e
}

func TestLG_1_RandomRoundTrip(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	types := []string{"", "put", "delete", "expire", "ready"}
	for range 1000 {
		e := randEntry(r)
		got, err := pbwire.UnmarshalEntry(pbwire.MarshalEntry(e))
		if err != nil || !entryEqual(got, e) {
			t.Fatalf("Entry %s came back as (%s, %v)", showEntry(e), showEntry(got), err)
		}
		ev := watch.Event{ID: r.Uint64() >> r.UintN(64), Type: types[r.IntN(len(types))], Entry: randEntry(r)}
		if ev.Entry.Key == "" {
			ev.Entry = store.Entry{}
		}
		back, err := pbwire.UnmarshalEvent(pbwire.MarshalEvent(ev))
		if err != nil || !eventEqual(back, ev) {
			t.Fatalf("Event %s came back as (%s, %v)", showEvent(ev), showEvent(back), err)
		}
	}
}

func FuzzLG_1_RoundTrip(f *testing.F) {
	for _, c := range entryGolden {
		f.Add(h(c.want), c.e.Key, c.e.Value, c.e.Version, int64(0), uint64(0), uint8(0))
	}
	for i, c := range eventGolden {
		f.Add(h(c.want), "k", []byte("v"), uint64(i), int64(i), c.ev.ID, uint8(i))
	}
	for _, u := range unknowns {
		f.Add(h(u.bytes+" 0a 01 6b"), "", []byte(nil), uint64(0), int64(-1), uint64(1), uint8(4))
	}
	f.Add(h("43 43 43 44 44 44"), "a", []byte{}, uint64(math.MaxUint64), int64(math.MinInt64), uint64(0), uint8(9))
	types := []string{"", "put", "delete", "expire", "ready", "bogus"}
	f.Fuzz(func(t *testing.T, data []byte, key string, value []byte, version uint64, exp int64, id uint64, typ uint8) {
		// Arbitrary bytes: no panic; whatever decodes re-encodes to a
		// stable canonical form.
		if e, err := pbwire.UnmarshalEntry(data); err == nil {
			b := pbwire.MarshalEntry(e)
			e2, err := pbwire.UnmarshalEntry(b)
			if err != nil || !entryEqual(e, e2) || !bytes.Equal(pbwire.MarshalEntry(e2), b) {
				t.Fatalf("Entry decoded from % x as %s re-encodes to % x, which decodes to (%s, %v)",
					data, showEntry(e), b, showEntry(e2), err)
			}
		}
		if ev, err := pbwire.UnmarshalEvent(data); err == nil {
			b := pbwire.MarshalEvent(ev)
			ev2, err := pbwire.UnmarshalEvent(b)
			if err != nil || !bytes.Equal(pbwire.MarshalEvent(ev2), b) ||
				(ev.Entry.Key != "" && !eventEqual(ev, ev2)) {
				t.Fatalf("Event decoded from % x as %s re-encodes to % x, which decodes to (%s, %v)",
					data, showEvent(ev), b, showEvent(ev2), err)
			}
		}
		if msg, err := pbwire.ReadGRPCMessage(bytes.NewReader(data), 1<<16); err == nil {
			var out bytes.Buffer
			if err := pbwire.WriteGRPCMessage(&out, msg); err != nil || !bytes.HasPrefix(data, out.Bytes()) {
				t.Fatalf("a gRPC message read from % x re-encodes to % x (%v)", data, out.Bytes(), err)
			}
		}
		// Structured values: every Entry and Event round-trips.
		e := store.Entry{Key: key, Value: value, Version: version}
		if exp != 0 {
			e.ExpiresAt = time.Unix(0, exp)
		}
		got, err := pbwire.UnmarshalEntry(pbwire.MarshalEntry(e))
		if err != nil || !entryEqual(got, e) {
			t.Fatalf("Entry %s came back as (%s, %v)", showEntry(e), showEntry(got), err)
		}
		ev := watch.Event{ID: id, Type: types[int(typ)%len(types)], Entry: e}
		want := ev
		if want.Type == "bogus" {
			want.Type = ""
		}
		if e.Key == "" {
			want.Entry = store.Entry{}
		}
		back, err := pbwire.UnmarshalEvent(pbwire.MarshalEvent(ev))
		if err != nil || !eventEqual(back, want) {
			t.Fatalf("Event %s came back as (%s, %v), want %s", showEvent(ev), showEvent(back), err, showEvent(want))
		}
	})
}
