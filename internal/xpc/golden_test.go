package xpc

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jen20/go-container/internal/mach"
)

// The files in testdata/libxpc are message bodies that the system libxpc
// sent for known dictionaries, captured from the Mach messages. Each
// dictionary has one key, "v", so that the key order is fixed. libxpc
// writes the keys of a larger dictionary in hash order.
//
// A body that libxpc sent out of line arrives padded to a page boundary.
// The files hold only the bytes that the root dictionary covers.

type goldenCase struct {
	name  string
	value func(t *testing.T) any
	// ports is the number of port descriptors that the message carries.
	ports int
}

func constant(v any) func(*testing.T) any { return func(*testing.T) any { return v } }

var goldenCases = []goldenCase{
	{"string", constant("abc"), 0},
	{"string4", constant("abcd"), 0},
	{"string_empty", constant(""), 0},
	{"int64", constant(int64(-2)), 0},
	{"uint64", constant(uint64(0x1122334455667788)), 0},
	{"bool_true", constant(true), 0},
	{"bool_false", constant(false), 0},
	{"double", constant(1.5), 0},
	{"date", constant(time.Unix(0, 1_700_000_000_000_000_000)), 0},
	{"null", constant(nil), 0},
	{"uuid", constant(UUID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}), 0},
	{"data", constant([]byte(`{"a":1}`)), 0},
	{"data_inline_max", constant(pattern(inlineDataMax)), 0},
	{"data_port", constant(pattern(inlineDataMax + 1)), 1},
	{"array", constant([]any{Dictionary{"k": "v"}, nil}), 0},
	{"array_empty", constant([]any{}), 0},
	{"dict", constant(Dictionary{"n": int64(1)}), 0},
	{"dict_empty", constant(Dictionary{}), 0},
	{"fd", func(*testing.T) any { return os.Stdout }, 1},
	{"endpoint", func(t *testing.T) any {
		l, err := Listen()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { checkClose(t, l) })
		return l.Endpoint()
	}, 1},
	{"ool_body", constant(strings.Repeat("x", 20000)), 0},
}

func readGolden(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "libxpc", name+".bin"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEncodeMatchesLibxpc(t *testing.T) {
	for _, tc := range goldenCases {
		t.Run(tc.name, func(t *testing.T) {
			body, ports, err := encodeMessage(Dictionary{"v": tc.value(t)})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := releasePorts(ports); err != nil {
					t.Errorf("release ports: %v", err)
				}
			}()
			if want := readGolden(t, tc.name); !bytes.Equal(body, want) {
				t.Errorf("body differs from libxpc\n got: % x\nwant: % x", head(body), head(want))
			}
			if len(ports) != tc.ports {
				t.Errorf("ports = %d, want %d", len(ports), tc.ports)
			}
		})
	}
}

func TestDecodeLibxpc(t *testing.T) {
	for _, tc := range goldenCases {
		if tc.ports > 0 {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeMessage(readGolden(t, tc.name), nil)
			if err != nil {
				t.Fatal(err)
			}
			if want := (Dictionary{"v": tc.value(t)}); !reflect.DeepEqual(got, want) {
				t.Errorf("decoded %#v, want %#v", got, want)
			}
		})
	}
}

func TestDecodeLibxpcLargeData(t *testing.T) {
	// The data travels in a memory entry port, which the decoder reads
	// and releases.
	want := pattern(inlineDataMax + 1)
	p, err := mach.NewMemoryEntry(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeMessage(readGolden(t, "data_port"), []mach.Port{p})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Data("v"), want) {
		t.Fatalf("data differs: got %d bytes", len(got.Data("v")))
	}
}

func TestDecodeLibxpcKeyOrder(t *testing.T) {
	got, err := decodeMessage(readGolden(t, "multi"), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := Dictionary{"route": "ping", "a": int64(1), "b": true, "zz": "last"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded %#v, want %#v", got, want)
	}
}

func TestDecodeReleasesUnusedPorts(t *testing.T) {
	wrongVersion := readGolden(t, "dict_empty")
	le.PutUint32(wrongVersion[4:], wireVersion+1)
	for _, tc := range []struct {
		name    string
		body    []byte
		wantErr bool
	}{
		{"short header", nil, true},
		{"wrong magic", make([]byte, 8), true},
		{"wrong version", wrongVersion, true},
		{"unused port", readGolden(t, "dict_empty"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := mach.NewMemoryEntry([]byte{0})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeMessage(tc.body, []mach.Port{p}); (err != nil) != tc.wantErr {
				t.Errorf("decode = %v, want error: %t", err, tc.wantErr)
			}
			if mach.Deallocate(p) == nil {
				t.Error("decoder left the port right open")
			}
		})
	}
}

// head returns at most the first 64 bytes of b, for error messages.
func head(b []byte) []byte { return b[:min(len(b), 64)] }
