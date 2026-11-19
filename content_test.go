package container

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jen20/go-container/internal/xpc"
)

func TestWriteBlob(t *testing.T) {
	s := &IngestSession{Dir: t.TempDir()}
	data := "hello, content store\n"
	digest, size, err := s.WriteBlob(strings.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(data))
	encoded := hex.EncodeToString(sum[:])
	if digest != "sha256:"+encoded || size != int64(len(data)) {
		t.Errorf("WriteBlob = %s, %d", digest, size)
	}
	if got, err := os.ReadFile(filepath.Join(s.Dir, encoded)); err != nil || string(got) != data {
		t.Errorf("blob file = %q, %v", got, err)
	}
	checkDir(t, s.Dir, encoded)

	// A failed read leaves no file that the server would ingest.
	failing := io.MultiReader(strings.NewReader("partial"), errReader{})
	if _, _, err := s.WriteBlob(failing); !errors.Is(err, errRead) {
		t.Errorf("WriteBlob of a failing reader gave %v", err)
	}
	checkDir(t, s.Dir, encoded)
}

func TestIngestCancelsWhenCompleteFails(t *testing.T) {
	c := NewClient()
	t.Cleanup(func() { checkClose(t, c) })
	l, err := xpc.Listen()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { checkClose(t, l) })
	c.images.conn, err = xpc.DialEndpoint(l.Endpoint())
	if err != nil {
		t.Fatal(err)
	}
	p, err := l.Accept(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { checkClose(t, p) })

	dir := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := c.Ingest(ctx, func(s *IngestSession) error {
			_, _, err := s.WriteBlob(strings.NewReader("blob"))
			cancel()
			return err
		})
		done <- err
	}()

	for _, step := range []struct {
		route string
		reply xpc.Dictionary
	}{
		{"contentIngestStart", xpc.Dictionary{"directory": dir, "ingestSessionId": "session"}},
		{"contentIngestCancel", xpc.Dictionary{}},
	} {
		m, err := p.Receive(time.Second)
		if err != nil {
			t.Fatalf("waiting for %s: %v", step.route, err)
		}
		if got := m.String(routeKey); got != step.route {
			t.Errorf("request route = %q, want %q", got, step.route)
		}
		if err := p.Reply(m, step.reply); err != nil {
			t.Error(err)
		}
		checkClose(t, m)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("Ingest returned %v, want context.Canceled", err)
	}
}

var errRead = errors.New("read failed")

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errRead }

func checkDir(t *testing.T, dir string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("directory has %v, want %v", names, want)
	}
}
