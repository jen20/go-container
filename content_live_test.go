package container

import (
	"bytes"
	"crypto/rand"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jen20/go-container/internal/testenv"
)

// The ingest tests add a blob of random bytes to the content store and
// remove it again. They need GO_CONTAINER_TEST_WRITE.

func randomBlob(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 4096)
	rand.Read(b)
	return b
}

func TestLiveContentSize(t *testing.T) {
	c := liveClient(t)
	size, err := c.ContentSize(liveContext(t))
	if err != nil {
		t.Fatal(err)
	}
	if size == 0 {
		t.Error("ContentSize is 0")
	}
}

func TestLiveIngestCompleteDelete(t *testing.T) {
	testenv.RequireWrite(t)
	c := liveClient(t)
	ctx := liveContext(t)
	blob := randomBlob(t)

	var digest string
	ingested, err := c.Ingest(ctx, func(s *IngestSession) error {
		var err error
		digest, _, err = s.WriteBlob(bytes.NewReader(blob))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded := strings.TrimPrefix(digest, "sha256:")
	t.Cleanup(func() {
		if _, _, err := c.DeleteContent(cleanupContext(t), []string{digest}); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	if len(ingested) != 1 || ingested[0] != encoded {
		t.Errorf("Ingest = %v, want [%s]", ingested, encoded)
	}

	path, err := c.ContentPath(ctx, digest)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, blob) {
		t.Errorf("stored blob differs: %v", err)
	}

	deleted, size, err := c.DeleteContent(ctx, []string{digest})
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || deleted[0] != encoded || size != uint64(len(blob)) {
		t.Errorf("DeleteContent = %v, %d", deleted, size)
	}
	if _, err := c.ContentPath(ctx, digest); !errors.Is(err, ErrNotFound) {
		t.Errorf("ContentPath after DeleteContent gave %v, want ErrNotFound", err)
	}
	if deleted, _, err := c.DeleteContent(ctx, []string{digest}); err != nil || len(deleted) != 0 {
		t.Errorf("second DeleteContent = %v, %v", deleted, err)
	}
	if _, _, err := c.DeleteContent(ctx, []string{"not-a-digest"}); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("DeleteContent of a bad digest gave %v, want ErrInvalidArgument", err)
	}
}

func TestLiveIngestCancel(t *testing.T) {
	testenv.RequireWrite(t)
	c := liveClient(t)
	ctx := liveContext(t)

	s, err := c.StartIngest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	digest, _, err := s.WriteBlob(bytes.NewReader(randomBlob(t)))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("session directory after Cancel: %v", err)
	}
	if _, err := c.ContentPath(ctx, digest); !errors.Is(err, ErrNotFound) {
		t.Errorf("ContentPath after Cancel gave %v, want ErrNotFound", err)
	}
	if err := s.Cancel(ctx); err != nil {
		t.Errorf("second Cancel: %v", err)
	}
	if _, err := s.Complete(ctx); err == nil {
		t.Error("Complete after Cancel succeeded")
	}

	// A failing fn cancels the session.
	errWrite := errors.New("write failed")
	var dir string
	_, err = c.Ingest(ctx, func(s *IngestSession) error {
		dir = s.Dir
		if _, _, err := s.WriteBlob(bytes.NewReader(randomBlob(t))); err != nil {
			return err
		}
		return errWrite
	})
	if !errors.Is(err, errWrite) {
		t.Errorf("Ingest gave %v, want errWrite", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("session directory after failed Ingest: %v", err)
	}
}
