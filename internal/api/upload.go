package api

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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
		if _, err := os.Stat(candidate); err != nil {
			return candidate
		}
		candidate = filepath.Join(dir, stem+"-"+strconv.Itoa(i)+ext)
	}
}

func (s *Server) handleAssetUpload(w http.ResponseWriter, r *http.Request) {
	p, err := s.DB.GetProject(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if p == nil {
		writeErr(w, xcerr.E(xcerr.CodeNotFound, "project not found", nil))
		return
	}
	name, ok := sanitizeImportName(r.URL.Query().Get("filename"))
	if !ok {
		writeErr(w, xcerr.E(xcerr.CodeValidation,
			"filename query parameter is required (a bare file name)", nil))
		return
	}
	if r.ContentLength > maxUploadBytes {
		writeErr(w, xcerr.E(xcerr.CodeResourceLimit,
			"upload exceeds the 8 GiB per-file bound", nil))
		return
	}

	importsDir := filepath.Join(s.Pipe.WS.ImportsDir(), p.ID)
	if err := os.MkdirAll(importsDir, 0o755); err != nil {
		writeErr(w, xcerr.E(xcerr.CodeInternal, "cannot prepare the imports directory", err))
		return
	}

	tmp, err := os.CreateTemp(importsDir, ".upload-*")
	if err != nil {
		writeErr(w, xcerr.E(xcerr.CodeInternal, "cannot stage the upload", err))
		return
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		_ = os.Remove(tmpName)
	}

	// Read one byte past the bound to detect overflow, then refuse.
	written, err := io.Copy(tmp, io.LimitReader(r.Body, maxUploadBytes+1))
	if err != nil {
		cleanup()
		writeErr(w, xcerr.E(xcerr.CodeInternal, "upload transfer failed", err))
		return
	}
	if written > maxUploadBytes {
		cleanup()
		writeErr(w, xcerr.E(xcerr.CodeResourceLimit,
			"upload exceeds the 8 GiB per-file bound", nil))
		return
	}
	if written == 0 {
		cleanup()
		writeErr(w, xcerr.E(xcerr.CodeValidation, "empty upload", nil))
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		writeErr(w, xcerr.E(xcerr.CodeInternal, "cannot finalize the staged upload", err))
		return
	}

	finalPath := uniqueImportPath(importsDir, name)
	if err := os.Rename(tmpName, finalPath); err != nil {
		_ = os.Remove(tmpName)
		writeErr(w, xcerr.E(xcerr.CodeInternal, "cannot land the upload", err))
		return
	}

	asset, err := s.Pipe.ImportAsset(p, finalPath)
	if err != nil {
		// Not user's original media — a failed probe means the copy is
		// unusable; remove it rather than littering imports/.
		_ = os.Remove(finalPath)
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"asset": asset})
}
