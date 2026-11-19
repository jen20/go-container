package container

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"

	"github.com/jen20/go-container/internal/xpc"
)

// This file is the client of the content store in the image service. The
// store keeps each blob in a file named by the hex encoding of its SHA-256
// digest. Requests accept a digest as "sha256:<hex>" or as the bare hex
// encoding. Replies name blobs by the bare hex encoding.

// DeleteContent removes the blobs with the given digests from the content
// store. It returns the hex encodings of the blobs that it removed and the
// space freed, in bytes. A digest with no blob is skipped. The server does
// not check whether an image uses a blob, so removing a blob can break an
// image.
func (c *Client) DeleteContent(ctx context.Context, digests []string) ([]string, uint64, error) {
	return c.deleteContent(ctx, "contentDelete", digests)
}

// DeleteContentExcept removes every blob from the content store except the
// blobs with the given digests. It returns the hex encodings of the blobs
// that it removed and the space freed, in bytes.
//
// If keep is empty, it removes all content, including the content of every
// image. To remove only the content that no image uses, call
// CleanUpOrphanedBlobs.
func (c *Client) DeleteContentExcept(ctx context.Context, keep []string) ([]string, uint64, error) {
	return c.deleteContent(ctx, "contentClean", keep)
}

func (c *Client) deleteContent(ctx context.Context, route string, digests []string) (_ []string, _ uint64, err error) {
	data, err := encodeJSON(emptyIfNil(digests))
	if err != nil {
		return nil, 0, err
	}
	reply, err := c.images.requestTimeout(ctx, route, xpc.Dictionary{"digests": data}, 0)
	if err != nil {
		return nil, 0, err
	}
	defer closeJoin(&err, reply)
	var deleted []string
	if err := decodeContentReply(reply, route, "failed to delete digests", &deleted); err != nil {
		return nil, 0, err
	}
	return deleted, reply.Uint64("imageSize"), nil
}

// ContentSize returns the disk space that the content store uses, in bytes.
// It counts the stored blobs and the files of active ingest sessions.
func (c *Client) ContentSize(ctx context.Context) (_ uint64, err error) {
	reply, err := c.images.requestTimeout(ctx, "contentSize", nil, 0)
	if err != nil {
		return 0, err
	}
	defer closeJoin(&err, reply)
	return reply.Uint64("imageSize"), nil
}

// IngestSession is a transaction that adds blobs to the content store. The
// caller writes blob files into Dir, then calls Complete to move them into
// the store, or Cancel to discard them. Each file must be named by the hex
// encoding of the SHA-256 digest of its content. The server does not check
// the names, so use WriteBlob, which names each file by its digest.
type IngestSession struct {
	// ID identifies the session to the server.
	ID string
	// Dir is the directory for the blob files of the session.
	Dir string

	c *Client
}

// StartIngest starts an ingest session.
func (c *Client) StartIngest(ctx context.Context) (_ *IngestSession, err error) {
	reply, err := c.images.requestTimeout(ctx, "contentIngestStart", nil, 0)
	if err != nil {
		return nil, err
	}
	defer closeJoin(&err, reply)
	s := &IngestSession{ID: reply.String("ingestSessionId"), Dir: reply.String("directory"), c: c}
	if s.ID == "" || s.Dir == "" {
		return nil, &Error{Code: CodeInternalError, Message: "failed create new ingest session"}
	}
	return s, nil
}

// WriteBlob copies r into a new blob file in the session directory and
// returns the digest of the blob, as "sha256:<hex>", and its size in bytes.
func (s *IngestSession) WriteBlob(r io.Reader) (digest string, size int64, err error) {
	// The server ingests every file in the directory, so the temporary
	// file must not stay behind if the write fails.
	f, err := os.CreateTemp(s.Dir, ".blob-*")
	if err != nil {
		return "", 0, err
	}
	defer func() {
		if err != nil {
			err = joinErrors(err, os.Remove(f.Name()))
		}
	}()
	h := sha256.New()
	size, err = io.Copy(f, io.TeeReader(r, h))
	if err = joinErrors(err, f.Close()); err != nil {
		return "", 0, err
	}
	encoded := hex.EncodeToString(h.Sum(nil))
	if err := os.Rename(f.Name(), filepath.Join(s.Dir, encoded)); err != nil {
		return "", 0, err
	}
	return "sha256:" + encoded, size, nil
}

// Complete moves the blob files of the session into the content store and
// ends the session. It returns the hex encodings of all the files in the
// session directory, including blobs that the store already had. If a move
// fails, the server removes the blobs that it moved, and the store does not
// change.
func (s *IngestSession) Complete(ctx context.Context) (_ []string, err error) {
	reply, err := s.c.images.requestTimeout(ctx, "contentIngestComplete", xpc.Dictionary{"ingestSessionId": s.ID}, 0)
	if err != nil {
		return nil, err
	}
	defer closeJoin(&err, reply)
	var ingested []string
	if err := decodeContentReply(reply, "contentIngestComplete", "failed to delete digests", &ingested); err != nil {
		return nil, err
	}
	return ingested, nil
}

// Cancel removes the session directory and ends the session. Cancel of a
// session that has ended does nothing.
func (s *IngestSession) Cancel(ctx context.Context) error {
	return s.c.images.send(ctx, "contentIngestCancel", xpc.Dictionary{"ingestSessionId": s.ID}, 0)
}

// Ingest starts an ingest session, calls fn to write blobs into it, and
// completes the session. If writing or completing fails, Ingest cancels the
// session and returns the error.
func (c *Client) Ingest(ctx context.Context, fn func(*IngestSession) error) ([]string, error) {
	s, err := c.StartIngest(ctx)
	if err != nil {
		return nil, err
	}
	var ingested []string
	if err = fn(s); err == nil {
		ingested, err = s.Complete(ctx)
		if err == nil {
			return ingested, nil
		}
	}
	// Cancel even if ctx is done, so the server removes the session directory.
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), DefaultTimeout)
	defer cancel()
	return ingested, joinErrors(err, s.Cancel(cctx))
}

// decodeContentReply decodes the list of digests in a content store reply.
// A missing list is an error with CodeInternalError and the given message.
func decodeContentReply(reply xpc.Dictionary, route, message string, out *[]string) error {
	if reply.Data("digests") == nil {
		return &Error{Code: CodeInternalError, Message: message}
	}
	return decodeReply(reply, route, "digests", out)
}
