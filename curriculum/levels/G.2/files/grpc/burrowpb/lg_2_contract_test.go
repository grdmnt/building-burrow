package burrowpb_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	_ "burrow/grpc/burrowpb"
	"burrow/pbwire"
	"burrow/store"
	"burrow/watch"
)

// The checks below read the generated descriptors, not the Go structs,
// so a mistake in kv.proto shows up as a message instead of a build
// failure.

func file(t *testing.T) protoreflect.FileDescriptor {
	t.Helper()
	fd, err := protoregistry.GlobalFiles.FindFileByPath("burrow/v1/kv.proto")
	if err != nil {
		t.Fatalf("burrowpb registers no file burrow/v1/kv.proto (%v). Keep the proto at "+
			"grpc/proto/burrow/v1/kv.proto and make proto/ the buf module root "+
			"(buf.yaml: modules: - path: proto), then run `buf generate`.", err)
	}
	return fd
}

func message(t *testing.T, name string) protoreflect.MessageDescriptor {
	t.Helper()
	md := file(t).Messages().ByName(protoreflect.Name(name))
	if md == nil {
		t.Fatalf("kv.proto has no message %s", name)
	}
	return md
}

func TestLG_2_File(t *testing.T) {
	fd := file(t)
	if fd.Package() != "burrow.v1" {
		t.Errorf("package is %q, want burrow.v1", fd.Package())
	}
	if fd.Syntax() != protoreflect.Proto3 {
		t.Errorf("syntax is %v, want proto3", fd.Syntax())
	}
	opts, _ := fd.Options().(*descriptorpb.FileOptions)
	gp := opts.GetGoPackage()
	if path, _, _ := strings.Cut(gp, ";"); path != "burrow/grpc/burrowpb" {
		t.Errorf("option go_package = %q, want \"burrow/grpc/burrowpb;burrowpb\"", gp)
	}
}

type field struct {
	num      protoreflect.FieldNumber
	name     string
	kind     protoreflect.Kind
	presence bool // explicit presence (proto3 optional)
	list     bool
	typ      string // full name of the message or enum type
}

func TestLG_2_Messages(t *testing.T) {
	entry := []field{{num: 1, name: "entry", kind: protoreflect.MessageKind, typ: "burrow.v1.Entry"}}
	cases := map[string][]field{
		"Entry": {
			{num: 1, name: "key", kind: protoreflect.StringKind},
			{num: 2, name: "value", kind: protoreflect.BytesKind},
			{num: 3, name: "version", kind: protoreflect.Uint64Kind},
			{num: 4, name: "expires_at_unix_nano", kind: protoreflect.Int64Kind},
		},
		"Event": {
			{num: 1, name: "id", kind: protoreflect.Uint64Kind},
			{num: 2, name: "type", kind: protoreflect.EnumKind, typ: "burrow.v1.EventType"},
			{num: 3, name: "entry", kind: protoreflect.MessageKind, typ: "burrow.v1.Entry"},
		},
		"GetRequest":  {{num: 1, name: "key", kind: protoreflect.StringKind}},
		"GetResponse": entry,
		"PutRequest": {
			{num: 1, name: "key", kind: protoreflect.StringKind},
			{num: 2, name: "value", kind: protoreflect.BytesKind},
			{num: 3, name: "if_version", kind: protoreflect.Uint64Kind, presence: true},
			{num: 4, name: "ttl_ms", kind: protoreflect.Int64Kind},
		},
		"PutResponse": entry,
		"DeleteRequest": {
			{num: 1, name: "key", kind: protoreflect.StringKind},
			{num: 2, name: "if_version", kind: protoreflect.Uint64Kind, presence: true},
		},
		"DeleteResponse": {},
		"TouchRequest": {
			{num: 1, name: "key", kind: protoreflect.StringKind},
			{num: 2, name: "ttl_ms", kind: protoreflect.Int64Kind},
		},
		"TouchResponse": entry,
		"KeysRequest":   {{num: 1, name: "prefix", kind: protoreflect.StringKind}},
		"KeysResponse":  {{num: 1, name: "keys", kind: protoreflect.StringKind, list: true}},
		"WatchRequest": {
			{num: 1, name: "prefix", kind: protoreflect.StringKind},
			{num: 2, name: "after", kind: protoreflect.Uint64Kind, presence: true},
		},
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			md := message(t, name)
			if md.Fields().Len() != len(want) {
				t.Errorf("%s has %d fields, want %d", name, md.Fields().Len(), len(want))
			}
			for _, w := range want {
				fd := md.Fields().ByNumber(w.num)
				if fd == nil {
					t.Errorf("%s has no field number %d (%s)", name, w.num, w.name)
					continue
				}
				if string(fd.Name()) != w.name || fd.Kind() != w.kind {
					t.Errorf("%s field %d is %s %s, want %s %s", name, w.num, fd.Kind(), fd.Name(), w.kind, w.name)
				}
				if fd.IsList() != w.list {
					t.Errorf("%s.%s: repeated = %v, want %v", name, w.name, fd.IsList(), w.list)
				}
				if w.kind != protoreflect.MessageKind && fd.HasPresence() != w.presence {
					if w.presence {
						t.Errorf("%s.%s must be `optional`: unset and 0 mean different things", name, w.name)
					} else {
						t.Errorf("%s.%s must not be `optional`: 0 is its \"not set\"", name, w.name)
					}
				}
				var typ protoreflect.FullName
				switch {
				case fd.Message() != nil:
					typ = fd.Message().FullName()
				case fd.Enum() != nil:
					typ = fd.Enum().FullName()
				}
				if string(typ) != w.typ {
					t.Errorf("%s.%s has type %q, want %q", name, w.name, typ, w.typ)
				}
			}
		})
	}
}

func TestLG_2_EventType(t *testing.T) {
	ed := file(t).Enums().ByName("EventType")
	if ed == nil {
		t.Fatal("kv.proto has no enum EventType")
	}
	want := []string{"EVENT_TYPE_UNSPECIFIED", "EVENT_TYPE_PUT", "EVENT_TYPE_DELETE", "EVENT_TYPE_EXPIRE", "EVENT_TYPE_READY"}
	if ed.Values().Len() != len(want) {
		t.Errorf("EventType has %d values, want %d", ed.Values().Len(), len(want))
	}
	for i, name := range want {
		v := ed.Values().ByNumber(protoreflect.EnumNumber(i))
		if v == nil || string(v.Name()) != name {
			t.Errorf("EventType value %d should be %s, got %v", i, name, v)
		}
	}
}

func TestLG_2_Service(t *testing.T) {
	sd := file(t).Services().ByName("KV")
	if sd == nil {
		t.Fatal("kv.proto has no service KV")
	}
	want := []struct{ name, in, out string }{
		{"Get", "GetRequest", "GetResponse"},
		{"Put", "PutRequest", "PutResponse"},
		{"Delete", "DeleteRequest", "DeleteResponse"},
		{"Touch", "TouchRequest", "TouchResponse"},
		{"Keys", "KeysRequest", "KeysResponse"},
		{"Watch", "WatchRequest", "Event"},
	}
	if sd.Methods().Len() != len(want) {
		t.Errorf("service KV has %d methods, want %d", sd.Methods().Len(), len(want))
	}
	for _, w := range want {
		m := sd.Methods().ByName(protoreflect.Name(w.name))
		if m == nil {
			t.Errorf("service KV has no rpc %s", w.name)
			continue
		}
		if got := string(m.Input().FullName()); got != "burrow.v1."+w.in {
			t.Errorf("rpc %s takes %s, want burrow.v1.%s", w.name, got, w.in)
		}
		if got := string(m.Output().FullName()); got != "burrow.v1."+w.out {
			t.Errorf("rpc %s returns %s, want burrow.v1.%s", w.name, got, w.out)
		}
		if m.IsStreamingClient() {
			t.Errorf("rpc %s must not take a stream", w.name)
		}
		if stream := w.name == "Watch"; m.IsStreamingServer() != stream {
			t.Errorf("rpc %s: server streaming = %v, want %v", w.name, m.IsStreamingServer(), stream)
		}
	}
}

// newMsg builds a burrow.v1 message through reflection, setting fields
// by name: a stand-in for the generated struct literal.
func newMsg(t *testing.T, name string, set map[string]protoreflect.Value) protoreflect.Message {
	t.Helper()
	mt, err := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName("burrow.v1." + name))
	if err != nil {
		t.Fatalf("no generated Go type for burrow.v1.%s: %v", name, err)
	}
	m := mt.New()
	for k, v := range set {
		fd := m.Descriptor().Fields().ByName(protoreflect.Name(k))
		if fd == nil {
			t.Fatalf("burrow.v1.%s has no field %s", name, k)
		}
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("setting burrow.v1.%s.%s: %v (check the field's type in kv.proto)", name, k, p)
				}
			}()
			m.Set(fd, v)
		}()
	}
	return m
}

func entryMsg(t *testing.T, e store.Entry) protoreflect.Message {
	set := map[string]protoreflect.Value{}
	if e.Key != "" {
		set["key"] = protoreflect.ValueOfString(e.Key)
	}
	if len(e.Value) > 0 {
		set["value"] = protoreflect.ValueOfBytes(e.Value)
	}
	if e.Version != 0 {
		set["version"] = protoreflect.ValueOfUint64(e.Version)
	}
	if !e.ExpiresAt.IsZero() {
		set["expires_at_unix_nano"] = protoreflect.ValueOfInt64(e.ExpiresAt.UnixNano())
	}
	return newMsg(t, "Entry", set)
}

var typeNumbers = map[string]protoreflect.EnumNumber{"put": 1, "delete": 2, "expire": 3, "ready": 4}

func eventMsg(t *testing.T, ev watch.Event) protoreflect.Message {
	set := map[string]protoreflect.Value{}
	if ev.ID != 0 {
		set["id"] = protoreflect.ValueOfUint64(ev.ID)
	}
	if n := typeNumbers[ev.Type]; n != 0 {
		set["type"] = protoreflect.ValueOfEnum(n)
	}
	if ev.Entry.Key != "" {
		set["entry"] = protoreflect.ValueOfMessage(entryMsg(t, ev.Entry))
	}
	return newMsg(t, "Event", set)
}

var entries = []store.Entry{
	{Key: "a", Value: []byte("1"), Version: 1},
	{Key: "k"},
	{Key: "user:42", Value: []byte{0, 1, 0xff, '\n', 0}, Version: 300,
		ExpiresAt: time.Unix(0, 1767225600123456789)},
	{Key: "日本", Value: bytes.Repeat([]byte("v"), 200), Version: 1 << 63},
	{Key: "old", Version: 2, ExpiresAt: time.Unix(-5, 0)},
}

func sameEntry(a, b store.Entry) bool {
	return a.Key == b.Key && bytes.Equal(a.Value, b.Value) && a.Version == b.Version &&
		a.ExpiresAt.IsZero() == b.ExpiresAt.IsZero() && a.ExpiresAt.Equal(b.ExpiresAt)
}

var det = proto.MarshalOptions{Deterministic: true}

func TestLG_2_EntryMatchesPbwire(t *testing.T) {
	for _, e := range entries {
		got, err := det.Marshal(entryMsg(t, e).Interface())
		if err != nil {
			t.Fatal(err)
		}
		want := pbwire.MarshalEntry(e)
		if !bytes.Equal(got, want) {
			t.Errorf("Entry %q: proto.Marshal gives\n  % x\npbwire.MarshalEntry gives\n  % x\n"+
				"the field numbers or types in kv.proto disagree with G.1", e.Key, got, want)
			continue
		}
		back, err := pbwire.UnmarshalEntry(got)
		if err != nil || !sameEntry(back, e) {
			t.Errorf("pbwire.UnmarshalEntry(proto.Marshal(%q)) = %+v, %v", e.Key, back, err)
		}
		m := newMsg(t, "Entry", nil)
		if err := proto.Unmarshal(want, m.Interface()); err != nil {
			t.Errorf("proto.Unmarshal of pbwire bytes for %q: %v", e.Key, err)
		} else if !proto.Equal(m.Interface(), entryMsg(t, e).Interface()) {
			t.Errorf("proto.Unmarshal of pbwire bytes for %q gives %v", e.Key, m.Interface())
		}
	}
}

func TestLG_2_EventMatchesPbwire(t *testing.T) {
	events := []watch.Event{
		{ID: 1, Type: "put", Entry: entries[0]},
		{ID: 7, Type: "delete", Entry: store.Entry{Key: "a"}},
		{ID: 1 << 40, Type: "expire", Entry: entries[2]},
		{ID: 5, Type: "ready"},
	}
	for _, ev := range events {
		got, err := det.Marshal(eventMsg(t, ev).Interface())
		if err != nil {
			t.Fatal(err)
		}
		want := pbwire.MarshalEvent(ev)
		if !bytes.Equal(got, want) {
			t.Errorf("Event %d %s: proto.Marshal gives\n  % x\npbwire.MarshalEvent gives\n  % x", ev.ID, ev.Type, got, want)
			continue
		}
		back, err := pbwire.UnmarshalEvent(got)
		if err != nil || back.ID != ev.ID || back.Type != ev.Type || !sameEntry(back.Entry, ev.Entry) {
			t.Errorf("pbwire.UnmarshalEvent(proto.Marshal(event %d)) = %+v, %v", ev.ID, back, err)
		}
	}
}

// goMod is the part of `go mod edit -json` the checks read.
type goMod struct {
	Module  struct{ Path string }
	Require []struct{ Path, Version string }
	Replace []struct {
		Old, New struct{ Path, Version string }
	}
	Tool []struct{ Path string }
}

// goModJSON reads grpc/go.mod with `go mod edit -json`.
func goModJSON(t *testing.T) (mod goMod) {
	t.Helper()
	cmd := exec.Command("go", "mod", "edit", "-json", filepath.Join("..", "go.mod"))
	cmd.Env = append(os.Environ(), "GOFLAGS=", "GOWORK=off")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go mod edit -json ../go.mod: %v", err)
	}
	if err := json.Unmarshal(out, &mod); err != nil {
		t.Fatal(err)
	}
	return mod
}

func required(t *testing.T, path string) string {
	t.Helper()
	for _, r := range goModJSON(t).Require {
		if r.Path == path {
			return r.Version
		}
	}
	t.Fatalf("grpc/go.mod doesn't require %s. Run `go mod tidy` in grpc/.", path)
	return ""
}

func TestLG_2_Module(t *testing.T) {
	mod := goModJSON(t)
	if mod.Module.Path != "burrow/grpc" {
		t.Errorf("grpc/go.mod declares module %q, want burrow/grpc", mod.Module.Path)
	}
	if v := required(t, "burrow"); v != "v0.0.0" {
		t.Errorf("grpc/go.mod requires burrow %s, want v0.0.0 (the replace supplies the code)", v)
	}
	replaced := false
	for _, r := range mod.Replace {
		if r.Old.Path == "burrow" {
			replaced = strings.TrimSuffix(r.New.Path, "/") == ".."
			if !replaced {
				t.Errorf("burrow is replaced by %q, want ../ (the root of this repo)", r.New.Path)
			}
		}
	}
	if !replaced && !t.Failed() {
		t.Errorf("grpc/go.mod needs `replace burrow => ../` so it builds against your root module")
	}
	tools := map[string]bool{}
	for _, tl := range mod.Tool {
		tools[tl.Path] = true
	}
	for _, p := range []string{"google.golang.org/protobuf/cmd/protoc-gen-go", "google.golang.org/grpc/cmd/protoc-gen-go-grpc"} {
		if !tools[p] {
			t.Errorf("grpc/go.mod has no `tool %s`. Add it with `go get -tool %s@latest`.", p, p)
		}
	}
}

func TestLG_2_BufGen(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "buf.gen.yaml"))
	if err != nil {
		t.Fatalf("no grpc/buf.gen.yaml: %v", err)
	}
	for _, plugin := range []string{"protoc-gen-go", "protoc-gen-go-grpc"} {
		// "tool", "protoc-gen-go" in a flow list, or - tool / - protoc-gen-go
		// in a block list.
		re := regexp.MustCompile(`(?m)tool"?[\s,\-]*"?` + regexp.QuoteMeta(plugin) + `(["\s\]]|$)`)
		if !re.Match(b) {
			t.Errorf("buf.gen.yaml should run %s through `go tool`: "+
				"local: [\"go\", \"tool\", \"%s\"]", plugin, plugin)
		}
	}
}

// generatedBy returns the plugin version in a generated file's header.
func generatedBy(t *testing.T, name, plugin string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("no burrowpb/%s: %v. Run `buf generate` in grpc/ and commit the output.", name, err)
	}
	if !bytes.Contains(b, []byte("Code generated by "+plugin+". DO NOT EDIT.")) {
		t.Errorf("%s has no \"Code generated by %s. DO NOT EDIT.\" header", name, plugin)
	}
	m := regexp.MustCompile(regexp.QuoteMeta(plugin) + ` (v\S+)`).FindSubmatch(b)
	if m == nil {
		t.Fatalf("%s doesn't name the %s version that wrote it", name, plugin)
	}
	return string(m[1])
}

func TestLG_2_Fresh(t *testing.T) {
	if got, want := generatedBy(t, "kv.pb.go", "protoc-gen-go"), required(t, "google.golang.org/protobuf"); got != want {
		t.Errorf("kv.pb.go was written by protoc-gen-go %s but the module uses google.golang.org/protobuf %s. "+
			"Generate with `buf generate` so `go tool protoc-gen-go` runs, not a protoc-gen-go from your PATH.", got, want)
	}
	if got, want := generatedBy(t, "kv_grpc.pb.go", "protoc-gen-go-grpc"), required(t, "google.golang.org/grpc/cmd/protoc-gen-go-grpc"); got != want {
		t.Errorf("kv_grpc.pb.go was written by protoc-gen-go-grpc %s but go.mod pins the tool at %s. "+
			"Run `buf generate` again.", got, want)
	}
}
