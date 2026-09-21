package timeline

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/xiabee/XCut/internal/workspace"
	"github.com/xiabee/XCut/internal/xcerr"
)

// maxTimelineBytes caps timeline file size (a timeline is <<1 MB by design).
const maxTimelineBytes = 8 << 20

// LoadFile reads, parses and structurally validates a timeline document.
// Content checks that need media metadata still run via Validate(lookup).
func LoadFile(path string) (*Timeline, error) {
	fi, err := statRetryable(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, xcerr.E(xcerr.CodeNotFound, "timeline file does not exist", err)
		}
		return nil, xcerr.E(xcerr.CodeInternal, "cannot access timeline file", err)
	}
	if fi.Size() > maxTimelineBytes {
		return nil, xcerr.E(xcerr.CodeValidation,
			fmt.Sprintf("timeline file too large (%d bytes)", fi.Size()), nil)
	}
	var b []byte
	if err := workspace.RetryTransient(func() error {
		var e error
		b, e = os.ReadFile(path)
		return e
	}); err != nil {
		return nil, xcerr.E(xcerr.CodeInternal, "cannot read timeline file", err)
	}
	tl := &Timeline{}
	if err := json.Unmarshal(b, tl); err != nil {
		return nil, xcerr.E(xcerr.CodeValidation, "corrupt timeline file", err)
	}
	if err := tl.Validate(nil); err != nil {
		return nil, err
	}
	return tl, nil
}

// statRetryable and the read above absorb the window in which another process
// is replacing this document's name: while it is, Windows answers
// ERROR_ACCESS_DENIED to opening the file at all, which used to surface as a
// 500 on a plain GET. workspace.RetryTransient waits out exactly that class of
// error, so a genuinely missing file is still a fast NotFound and a corrupt one
// still fails on the first parse.
func statRetryable(path string) (os.FileInfo, error) {
	var fi os.FileInfo
	err := workspace.RetryTransient(func() error {
		var e error
		fi, e = os.Stat(path)
		return e
	})
	return fi, err
}
