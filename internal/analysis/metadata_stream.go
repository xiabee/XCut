package analysis

import (
	"bytes"
	"strconv"
	"strings"
)

// metadataCollector accumulates ffmpeg `metadata=print:file=-` output line
// by line. It is the StreamStdout sink for the analyzers: stdout size grows
// with media duration (per-frame metadata lines), so parsing must be
// incremental — the 1 MB Run capture silently kept only the tail of a long
// recording's samples, producing feature tracks that quietly started
// (in analyzer time) tens of minutes into the asset.
type metadataCollector struct {
	wanted    map[string]bool
	samples   map[string][]Sample
	pendingT  float64
	partial   []byte // trailing bytes of a line split across chunks
	bytesSeen int64
}

func newMetadataCollector(keys ...string) *metadataCollector {
	wanted := make(map[string]bool, len(keys))
	for _, k := range keys {
		wanted[k] = true
	}
	return &metadataCollector{
		wanted:   wanted,
		samples:  make(map[string][]Sample, len(keys)),
		pendingT: -1,
	}
}

// sink consumes one streamed chunk. Chunks may split lines anywhere; the
// incomplete tail is held until the next chunk (or finish).
func (c *metadataCollector) sink(chunk []byte) error {
	c.bytesSeen += int64(len(chunk))
	data := append(c.partial, chunk...)
	lines := bytes.Split(data, []byte("\n"))
	// Consume the complete lines BEFORE rebinding partial: lines alias
	// data's array, and copying the tail over it would clobber them.
	for _, l := range lines[:len(lines)-1] {
		c.line(string(l))
	}
	c.partial = append(c.partial[:0], lines[len(lines)-1]...)
	return nil
}

// finish flushes the final partial line and returns the accumulated samples.
func (c *metadataCollector) finish() (map[string][]Sample, error) {
	if len(c.partial) > 0 {
		c.line(string(c.partial))
		c.partial = nil
	}
	return c.samples, nil
}

// line applies one complete output line (same grammar as
// parseMetadataPrintKeys).
func (c *metadataCollector) line(line string) {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "frame:") {
		if t, ok := extractPtsTime(line); ok {
			c.pendingT = t
		} else {
			c.pendingT = -1
		}
		return
	}
	if c.pendingT < 0 {
		return
	}
	if k, v, ok := strings.Cut(line, "="); ok && c.wanted[strings.TrimSpace(k)] {
		// ParseFloat errors are tolerated line-wise, matching the buffer
		// parser: a single malformed print must not kill an analysis.
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			key := strings.TrimSpace(k)
			c.samples[key] = append(c.samples[key], Sample{T: c.pendingT, V: f})
			c.pendingT = -1
		}
	}
}
