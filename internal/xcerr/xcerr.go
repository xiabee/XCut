// Package xcerr defines XCut's typed error model.
//
// Every error crossing a module or user boundary should carry an xcerr Code so
// callers (CLI, future HTTP API) can map failures to stable, user-understandable
// categories without parsing messages. Technical causes stay in the wrapped
// error chain and in logs; Code+Message is what users see.
package xcerr

import (
	"errors"
	"fmt"
)

// Code is a stable, machine-readable error category.
type Code string

const (
	CodeValidation       Code = "validation"
	CodeConflict         Code = "conflict"
	CodeNotFound         Code = "not_found"
	CodeUnsupportedMedia Code = "unsupported_media"
	CodeResourceLimit    Code = "resource_limit"
	CodeFFmpegFailure    Code = "ffmpeg_failure"
	CodeAnalyzerFailure  Code = "analyzer_failure"
	CodeRenderFailure    Code = "render_failure"
	CodeStorageFailure   Code = "storage_failure"
	CodeCancelled        Code = "cancelled"
	CodeInternal         Code = "internal"
)

// Error is the canonical XCut error type.
type Error struct {
	Code    Code
	Message string // safe for display; must not embed secrets or raw internal paths
	Cause   error
}

func (e *Error) Error() string {
	if e.Cause == nil {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
}

func (e *Error) Unwrap() error { return e.Cause }

// E builds an *Error. message should be user-presentable; put technical detail
// in cause (it stays visible in logs via %v of the chain).
func E(code Code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, Cause: cause}
}

// CodeOf returns the first *Error code in the chain, defaulting to internal.
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) && e.Code != "" {
		return e.Code
	}
	return CodeInternal
}

// IsCode reports whether the chain carries exactly the given code.
func IsCode(err error, code Code) bool {
	return CodeOf(err) == code
}

// UserMessage returns the display-safe message for an error.
func UserMessage(err error) string {
	var e *Error
	if errors.As(err, &e) && e.Message != "" {
		return e.Message
	}
	return "internal error"
}
