package container

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ErrorCode is the code of an API server error.
type ErrorCode string

// Error codes that the API server sends.
const (
	// The API server sends a code as its string description, so the
	// values are those descriptions.

	CodeUnknown         ErrorCode = "unknown"
	CodeInvalidArgument ErrorCode = "invalidArgument"
	CodeInternalError   ErrorCode = "internalError"
	CodeExists          ErrorCode = "exists"
	CodeNotFound        ErrorCode = "notFound"
	CodeCancelled       ErrorCode = "cancelled"
	CodeInvalidState    ErrorCode = "invalidState"
	CodeEmpty           ErrorCode = "empty"
	CodeTimeout         ErrorCode = "timeout"
	CodeUnsupported     ErrorCode = "unsupported"
	CodeInterrupted     ErrorCode = "interrupted"
)

// Error is an error from the API server, or a client error that has the same
// form.
type Error struct {
	Code    ErrorCode
	Message string
	cause   error
}

// Sentinel errors for use with errors.Is. An *Error matches a sentinel when
// the codes are equal.
var (
	ErrNotFound        = &Error{Code: CodeNotFound}
	ErrExists          = &Error{Code: CodeExists}
	ErrInvalidArgument = &Error{Code: CodeInvalidArgument}
	ErrInvalidState    = &Error{Code: CodeInvalidState}
	ErrUnsupported     = &Error{Code: CodeUnsupported}
	ErrInterrupted     = &Error{Code: CodeInterrupted}
	ErrTimeout         = &Error{Code: CodeTimeout}
)

func (e *Error) Error() string {
	return fmt.Sprintf("container: %s: %s", e.Code, e.Message)
}

// Unwrap returns the underlying error, if any.
func (e *Error) Unwrap() error { return e.cause }

// Is reports whether target is a sentinel *Error with the same code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Message == "" && t.cause == nil && t.Code == e.Code
}

// decodeServerError decodes the JSON error payload that the server puts
// under com.apple.container.xpc.error.
func decodeServerError(data []byte) error {
	var payload struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return &Error{Code: CodeInternalError, Message: "received a malformed error payload from the XPC peer", cause: err}
	}
	return &Error{Code: ErrorCode(payload.Code), Message: payload.Message}
}

// joinErrors is errors.Join, but it returns a single non-nil error
// unchanged, so that callers can still compare it or check its type. Use it
// to add the errors of cleanup steps to the error that caused them.
func joinErrors(errs ...error) error {
	var nonNil []error
	for _, err := range errs {
		if err != nil {
			nonNil = append(nonNil, err)
		}
	}
	switch len(nonNil) {
	case 0:
		return nil
	case 1:
		return nonNil[0]
	}
	return errors.Join(nonNil...)
}

// closeJoin closes c and adds a failure to *errp. Use it with defer in a
// function that has a named error result.
func closeJoin(errp *error, c io.Closer) {
	*errp = joinErrors(*errp, c.Close())
}
