package api

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/xiabee/XCut/internal/xcerr"
)

// Web upload import: browsers cannot hand the server a local path (the
// path-import endpoint stays for that), so the client sends the file
// CONTENT and the server lands a copy under <workspace>/imports/<project>/
// before running the standard probe+fingerprint import. The user's
// original file is never touched; a failed probe deletes the copy.

// maxUploadBytes is the hard sanity bound for one upload. Localhost
// transfer makes even large media cheap, but an unbounded read is exactly
// the growth axis the resource policy forbids.
const maxUploadBytes = 8 << 30 // 8 GiB

// uploadIdleWindow re-arms the connection read deadline after every copied
// chunk. The server-level ReadTimeout (cli/serve.go) bounds a request's
// TOTAL read time, which a multi-GiB upload on a slow disk cannot fit —
// the body only advances as fast as the disk accepts bytes. So for uploads
// the total-time bound becomes an idle-time bound: progress re-arms the
// window, a stalled client still hits it. Matches serve.go's ReadTimeout;
// a var so the deadline test can shrink it.
var uploadIdleWindow = 30 * time.Second

// uploadChunkSize is the copy granularity for deadline re-arming.
const uploadChunkSize = 1 << 20 // 1 MiB

// copyUploadBody streams the request body into dst with the read-deadline
// heartbeat, capped at max+1 bytes (one byte past the limit lets the caller
// detect overflow). Setting the deadline is best-effort: transports without
// deadline support (in-memory recorders) just keep the server's fixed
// total-time bound.
func copyUploadBody(w http.ResponseWriter, dst io.Writer, body io.Reader, max int64) (int64, error) {
	rc := http.NewResponseController(w)
	buf := make([]byte, uploadChunkSize)
	var written int64
	for {
		_ = rc.SetReadDeadline(time.Now().Add(uploadIdleWindow))
		n, rerr := body.Read(buf)
		if n > 0 {
			written += int64(n)
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return written, werr
			}
		}
		if rerr == io.EOF {
			return written, nil
		}
		if rerr != nil {
			return written, rerr
		}
		if written > max {
			return written, nil
		}
	}
}

// sanitizeImportName reduces a client-supplied filename to a bare name
// that cannot escape the imports directory: no path components, no
// control characters, never empty. The extension is preserved (ffprobe,
// not the name, decides whether the content is media).
func sanitizeImportName(name string) (string, bool) {
	base := filepath.Base(strings.TrimSpace(name))
	if base == "." || base == ".." || base == "/" || base == "\\" {
		return "", false
	}
	var b strings.Builder
	for _, r := range base {
		if r < 0x20 || r == ':' {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	out = strings.TrimPrefix(out, ".")
	if out == "" {
		return "", false
	}
	return out, true
}

// uniqueImportPath returns dir/name, or dir/n-1.ext, n-2.ext ... for the
// first free slot (uploads never overwrite an existing copy).
func uniqueImportPath(dir, name string) string {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	candidate := filepath.Join(dir, name)
	for i := 1; ; i++ {
		// #nosec G703 -- callers pass sanitizeImportName output (no
		// separators); the final sink is additionally guarded by
		// ensureInsideImports.
		if _, err := os.Stat(candidate); err != nil {
			return candidate
		}
		candidate = filepath.Join(dir, stem+"-"+strconv.Itoa(i)+ext)
	}
}

// ensureInsideImports re-derives the traversal invariant at the sink, in
// executable form: the sanitized name carries no separators, so the joined
// path must still resolve under the imports directory. This is the check
// the #nosec annotations below lean on — gosec's taint flow cannot see
// through sanitizeImportName; this runtime guard does not have to.
func ensureInsideImports(importsDir, finalPath string) error {
	root := filepath.Clean(importsDir) + string(os.PathSeparator)
	clean := filepath.Clean(finalPath)
	if !strings.HasPrefix(clean, root) || clean == filepath.Clean(importsDir) {
		return xcerr.E(xcerr.CodeValidation, "upload name escapes the imports directory", nil)
	}
	return nil
}

func (s *Server) handleAssetUpload(w http.ResponseWriter, r *http.Request) {
	// The upload's one response is written only after the whole body copy
	// and the import probe — far past the server's absolute WriteTimeout,
	// which the net/http server arms once when the request headers are
	// read. Without the write-idle re-arm the transfer would land the file
	// and create the asset row while the 201 died on the expired deadline:
	// the browser reports "network error" and the user's retry duplicates
	// the import. Same treatment the streaming endpoints get: every
	// response write re-arms the deadline, a reader that stops consuming
	// still trips the window one span after the last delivered byte.
	w = &writeIdleWriter{ResponseWriter: w, rc: http.NewResponseController(w), window: streamIdleWindow}

	p, err := s.DB.GetProject(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if p == nil {
		s.writeErr(w, r, xcerr.E(xcerr.CodeNotFound, "project not found", nil))
		return
	}
	name, ok := sanitizeImportName(r.URL.Query().Get("filename"))
	if !ok {
		s.writeErr(w, r, xcerr.E(xcerr.CodeValidation,
			"filename query parameter is required (a bare file name)", nil))
		return
	}
	if r.ContentLength > maxUploadBytes {
		s.writeErr(w, r, xcerr.E(xcerr.CodeResourceLimit,
			"upload exceeds the 8 GiB per-file bound", nil))
		return
	}

	// p.ID is a server-generated project id read back from storage after
	// the GetProject existence check, not client text.
	importsDir := filepath.Join(s.Pipe.WS.ImportsDir(), p.ID) // #nosec G703 -- see previous line
	if err := os.MkdirAll(importsDir, 0o755); err != nil {    // #nosec G703 -- importsDir is workspace-root + server-generated project id
		s.writeErr(w, r, xcerr.E(xcerr.CodeInternal, "cannot prepare the imports directory", err))
		return
	}

	tmp, err := os.CreateTemp(importsDir, ".upload-*")
	if err != nil {
		s.writeErr(w, r, xcerr.E(xcerr.CodeInternal, "cannot stage the upload", err))
		return
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		_ = os.Remove(tmpName) // #nosec G703 -- server-generated staging path (os.CreateTemp), not user input
	}

	// Stream with the read-deadline heartbeat: progress keeps the
	// connection alive no matter how long the whole transfer takes; one
	// byte past the bound is still read to detect overflow.
	written, err := copyUploadBody(w, tmp, io.LimitReader(r.Body, maxUploadBytes+1), maxUploadBytes)
	if err != nil {
		cleanup()
		s.writeErr(w, r, xcerr.E(xcerr.CodeInternal, "upload transfer failed", err))
		return
	}
	if written > maxUploadBytes {
		cleanup()
		s.writeErr(w, r, xcerr.E(xcerr.CodeResourceLimit,
			"upload exceeds the 8 GiB per-file bound", nil))
		return
	}
	if written == 0 {
		cleanup()
		s.writeErr(w, r, xcerr.E(xcerr.CodeValidation, "empty upload", nil))
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName) // #nosec G703 -- server-generated staging path, not user input
		s.writeErr(w, r, xcerr.E(xcerr.CodeInternal, "cannot finalize the staged upload", err))
		return
	}

	// Landing is serialized so the never-overwrite contract holds under
	// concurrency: two uploads with the same name must land as distinct
	// files, and pick-free-slot + rename is only atomic inside the mutex
	// (this process is imports/' only writer).
	s.uploadLandMu.Lock()
	finalPath := uniqueImportPath(importsDir, name)
	if err := ensureInsideImports(importsDir, finalPath); err != nil {
		s.uploadLandMu.Unlock()
		_ = os.Remove(tmpName) // #nosec G703 -- server-generated staging path, not user input
		s.writeErr(w, r, err)
		return
	}
	// #nosec G703 -- finalPath is Join(importsDir, sanitizeImportName(name));
	// the sanitized name contains no separators and ensureInsideImports just
	// re-checked the prefix at this sink.
	if err := os.Rename(tmpName, finalPath); err != nil {
		s.uploadLandMu.Unlock()
		_ = os.Remove(tmpName) // #nosec G703 -- server-generated staging path, not user input
		s.writeErr(w, r, xcerr.E(xcerr.CodeInternal, "cannot land the upload", err))
		return
	}
	s.uploadLandMu.Unlock()

	asset, err := s.Pipe.ImportAsset(p, finalPath)
	if err != nil {
		// Not user's original media — a failed probe means the copy is
		// unusable; remove it rather than littering imports/.
		_ = os.Remove(finalPath)
		s.writeErr(w, r, err)
		return
	}
	if s.Log != nil {
		s.Log.Info("upload imported",
			"project_id", p.ID, "name", name, "bytes", written, "asset_id", asset.ID)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"asset": asset})
}
