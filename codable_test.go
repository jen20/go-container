package container

import (
	"encoding/json"
	"errors"
	"net/netip"
	"testing"
	"time"
)

func TestDateRoundTrip(t *testing.T) {
	const wire = "816494007.866992"
	var d Date
	if err := json.Unmarshal([]byte(wire), &d); err != nil {
		t.Fatal(err)
	}
	// The server sends a date as a float64, which at this size resolves
	// about 0.1 µs.
	want := time.Date(2026, 11, 16, 3, 53, 27, 866992000, time.UTC)
	if diff := d.Sub(want).Abs(); diff > time.Microsecond {
		t.Fatalf("decoded %v, want %v", d.Time, want)
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != wire {
		t.Fatalf("encoded %s, want %s", b, wire)
	}
}

func TestDateBeforeReference(t *testing.T) {
	var d Date
	if err := json.Unmarshal([]byte("-978307200"), &d); err != nil {
		t.Fatal(err)
	}
	if !d.Equal(time.Unix(0, 0)) {
		t.Fatalf("decoded %v, want the Unix epoch", d.Time)
	}
}

func TestEnums(t *testing.T) {
	tests := []struct {
		name string
		v    any
		json string
	}{
		{"user id", User{UID: 501, GID: 20}, `{"id":{"gid":20,"uid":501}}`},
		{"user raw", User{Name: "nobody"}, `{"raw":{"userString":"nobody"}}`},
		{"tmpfs", FSType{Kind: FSTmpfs}, `{"tmpfs":{}}`},
		{"virtiofs", FSType{Kind: FSVirtiofs}, `{"virtiofs":{}}`},
		{
			"block",
			FSType{Kind: FSBlock, Format: "ext4", Cache: CacheAuto, Sync: SyncFull},
			`{"block":{"cache":{"auto":{}},"format":"ext4","sync":{"full":{}}}}`,
		},
		{
			"volume",
			FSType{Kind: FSVolume, Name: "data", Format: "ext4", Cache: CacheOn, Sync: SyncFsync},
			`{"volume":{"cache":{"on":{}},"format":"ext4","name":"data","sync":{"fsync":{}}}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := json.Marshal(tt.v)
			if err != nil {
				t.Fatal(err)
			}
			if got := canonical(t, b); got != canonical(t, []byte(tt.json)) {
				t.Fatalf("encoded %s, want %s", got, tt.json)
			}
		})
	}
	var u User
	if err := json.Unmarshal([]byte(`{"id":{"uid":1,"gid":2}}`), &u); err != nil || u != (User{UID: 1, GID: 2}) {
		t.Fatalf("decoded %+v, %v", u, err)
	}
	var fs FSType
	if err := json.Unmarshal([]byte(`{"volume":{"name":"v","format":"ext4","cache":{"off":{}},"sync":{"nosync":{}}}}`), &fs); err != nil {
		t.Fatal(err)
	}
	if fs != (FSType{Kind: FSVolume, Name: "v", Format: "ext4", Cache: CacheOff, Sync: SyncNosync}) {
		t.Fatalf("decoded %+v", fs)
	}
}

func TestListFiltersEncoding(t *testing.T) {
	b, err := json.Marshal(ListFilters{})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"ids":[],"labels":{}}` {
		t.Fatalf("encoded %s", b)
	}
	b, _ = json.Marshal(ListFilters{IDs: []string{"a"}, Status: StatusRunning})
	if string(b) != `{"ids":["a"],"status":"running","labels":{}}` {
		t.Fatalf("encoded %s", b)
	}
}

func TestFilePermissions(t *testing.T) {
	for _, in := range []string{`420`, `{"rawValue":420}`} {
		var p FilePermissions
		if err := json.Unmarshal([]byte(in), &p); err != nil || p != 0o644 {
			t.Fatalf("%s: decoded %o, %v", in, p, err)
		}
	}
}

func TestServerError(t *testing.T) {
	err := decodeServerError([]byte(`{"code":"notFound","message":"container x not found"}`))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v does not match ErrNotFound", err)
	}
	if errors.Is(err, ErrExists) {
		t.Fatal("matched ErrExists")
	}
	if err.Error() != "container: notFound: container x not found" {
		t.Fatalf("message %q", err.Error())
	}
	err = decodeServerError([]byte(`not json`))
	if !errors.Is(err, &Error{Code: CodeInternalError}) {
		t.Fatalf("malformed payload gave %v", err)
	}
}

// snapshotJSON is a containerList entry in the form that the 1.7.1 server
// encodes. The values are made up.
const snapshotJSON = `{
  "configuration": {
    "id": "web",
    "image": {"reference": "docker.io/library/nginx:latest",
      "descriptor": {"mediaType": "application/vnd.oci.image.index.v1+json",
        "digest": "sha256:0000000000000000000000000000000000000000000000000000000000000000", "size": 10229}},
    "mounts": [
      {"type": {"tmpfs": {}}, "destination": "/run", "options": [], "source": ""},
      {"type": {"virtiofs": {}}, "source": "/Users/example/src", "destination": "/src", "options": ["ro"]},
      {"type": {"volume": {"name": "data", "format": "ext4", "cache": {"auto": {}}, "sync": {"fsync": {}}}},
        "source": "/var/lib/data.img", "destination": "/data", "options": []}
    ],
    "publishedPorts": [{"hostAddress": "0.0.0.0", "hostPort": 8080, "containerPort": 80, "proto": "tcp", "count": 1}],
    "publishedSockets": [],
    "labels": {"app": "web"},
    "sysctls": {},
    "networks": [{"network": "default", "options": {"hostname": "web"}}],
    "dns": {"nameservers": ["192.168.64.1"], "domain": "test", "searchDomains": [], "options": []},
    "rosetta": true,
    "initProcess": {"executable": "/docker-entrypoint.sh", "arguments": ["nginx", "-g", "daemon off;"],
      "environment": ["PATH=/usr/bin"], "workingDirectory": "/", "terminal": false,
      "user": {"id": {"uid": 0, "gid": 0}}, "supplementalGroups": [], "rlimits": []},
    "platform": {"os": "linux", "architecture": "arm64"},
    "resources": {"cpus": 4, "memoryInBytes": 1073741824, "cpuOverhead": 1},
    "runtimeHandler": "container-runtime-linux",
    "virtualization": false, "ssh": false, "readOnly": false, "useInit": false,
    "capAdd": [], "capDrop": [],
    "creationDate": 816494007.866992
  },
  "status": "running",
  "networks": [{"network": "default", "hostname": "web", "ipv4Address": "192.168.64.3/24",
    "ipv4Gateway": "192.168.64.1", "macAddress": "02:42:ac:11:00:02"}],
  "startedDate": 816494010.5
}`

func TestDecodeSnapshot(t *testing.T) {
	var s Snapshot
	if err := json.Unmarshal([]byte(snapshotJSON), &s); err != nil {
		t.Fatal(err)
	}
	if s.ID() != "web" || s.Status != StatusRunning {
		t.Fatalf("id %q status %q", s.ID(), s.Status)
	}
	m := s.Configuration.Mounts
	if len(m) != 3 || m[0].Type.Kind != FSTmpfs || m[1].Type.Kind != FSVirtiofs || m[2].Type.Name != "data" {
		t.Fatalf("mounts %+v", m)
	}
	if s.Networks[0].IPv4Address != netip.MustParsePrefix("192.168.64.3/24") {
		t.Fatalf("address %v", s.Networks[0].IPv4Address)
	}
	if s.StartedDate == nil || s.StartedDate.Sub(s.Configuration.CreationDate.Time) < 2*time.Second {
		t.Fatalf("started %v", s.StartedDate)
	}
	if s.Configuration.MaskedPaths != nil {
		t.Fatal("missing maskedPaths decoded as non-nil")
	}
}

// canonical returns b re-encoded with sorted keys and no space.
func canonical(t *testing.T, b []byte) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("invalid JSON %s: %v", b, err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestZeroValuesEncodeLikeServer(t *testing.T) {
	b, err := json.Marshal(Configuration{ID: "x"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"mounts", "publishedPorts", "publishedSockets", "networks", "capAdd", "capDrop"} {
		if s, ok := m[k].([]any); !ok || len(s) != 0 {
			t.Errorf("%s encoded as %v, want []", k, m[k])
		}
	}
	for _, k := range []string{"labels", "sysctls"} {
		if s, ok := m[k].(map[string]any); !ok || len(s) != 0 {
			t.Errorf("%s encoded as %v, want {}", k, m[k])
		}
	}
	for _, k := range []string{"maskedPaths", "readonlyPaths", "dns", "shmSize", "stopSignal"} {
		if _, ok := m[k]; ok {
			t.Errorf("nil %s was encoded", k)
		}
	}
	proc := m["initProcess"].(map[string]any)
	if s, ok := proc["arguments"].([]any); !ok || len(s) != 0 {
		t.Errorf("initProcess.arguments encoded as %v", proc["arguments"])
	}
	b, _ = json.Marshal(Configuration{MaskedPaths: []string{}})
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if s, ok := m["maskedPaths"].([]any); !ok || len(s) != 0 {
		t.Errorf("empty maskedPaths encoded as %v, want []", m["maskedPaths"])
	}
}
