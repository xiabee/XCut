package media

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"os"

	"github.com/xiabee/XCut/internal/xcerr"
)

// fingerprintSample is how many bytes we hash from the head and tail of a
// file. 64 KiB catches common edits while keeping cost constant regardless of
// file size (DECISIONS D9).
const (
	fingerprintSample = 64 * 1024
)

// Fingerprint returns a fast content identity for path:
// SHA256(size || mtime(ns) || head ≤64KiB || tail ≤64KiB).
//
// It is NOT a cryptographic guarantee of full content identity; it trades a
// bounded IO cost (≤128 KiB regardless of file size) for a negligible
// collision-with-edit risk. Full hashing is a future optional verify step.
// Same input file ⇒ same value across runs; any content, size, or mtime change
// invalidates it.
func Fingerprint(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", xcerr.E(xcerr.CodeNotFound, "file does not exist", err)
		}
		return "", xcerr.E(xcerr.CodeValidation, "cannot access file", err)
	}

	h := sha256.New()
	var meta [16]byte
	binary.LittleEndian.PutUint64(meta[0:8], uint64(fi.Size())) // #nosec G115 -- hash input only: a negative stat value wraps consistently and never enters arithmetic
	binary.LittleEndian.PutUint64(meta[8:16], uint64(fi.ModTime().UnixNano()))
	h.Write(meta[:])

	f, err := os.Open(path)
	if err != nil {
		return "", xcerr.E(xcerr.CodeInternal, "cannot open file", err)
	}
	defer f.Close()

	size := fi.Size()
	head := min64(size, fingerprintSample)
	if _, err := io.CopyN(h, f, head); err != nil && err != io.EOF {
		return "", xcerr.E(xcerr.CodeInternal, "cannot read file head", err)
	}
	if size > fingerprintSample {
		// Overlap with the head region for files < 128 KiB is fine: the hash
		// stays deterministic and content-sensitive.
		if _, err := f.Seek(-fingerprintSample, io.SeekEnd); err != nil {
			return "", xcerr.E(xcerr.CodeInternal, "cannot seek file tail", err)
		}
		if _, err := io.CopyN(h, f, fingerprintSample); err != nil && err != io.EOF {
			return "", xcerr.E(xcerr.CodeInternal, "cannot read file tail", err)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
