package eval

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/xiabee/XCut/internal/xcerr"
)

// Manifest is an evaluation run description: labeled media cases.
// Media paths are resolved relative to the manifest file's directory.
type Manifest struct {
	Version int    `json:"version"`
	Cases   []Case `json:"cases"`
}

// Case is one labeled media file.
type Case struct {
	Name     string  `json:"name"`
	Media    string  `json:"media"`
	Style    string  `json:"style,omitempty"` // style preset; caller applies a default
	Expected []Range `json:"expected"`
}

// maxManifestBytes caps manifest size (a manifest is small text by design).
const maxManifestBytes = 4 << 20

// LoadManifest reads and validates a manifest file. Media paths stay
// relative; ResolveMedia turns them into absolute paths at run time.
func LoadManifest(path string) (*Manifest, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, xcerr.E(xcerr.CodeNotFound, "cannot read eval manifest", err)
	}
	if fi.Size() > maxManifestBytes {
		return nil, xcerr.E(xcerr.CodeValidation,
			fmt.Sprintf("eval manifest too large (%d bytes)", fi.Size()), nil)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, xcerr.E(xcerr.CodeInternal, "cannot read eval manifest", err)
	}
	m, err := ParseManifest(b)
	if err != nil {
		return nil, err
	}
	// Resolve media paths against the manifest directory.
	dir := filepath.Dir(path)
	for i := range m.Cases {
		if rel := m.Cases[i].Media; rel != "" && !filepath.IsAbs(rel) {
			m.Cases[i].Media = filepath.Join(dir, rel)
		}
	}
	return m, nil
}

// ParseManifest decodes and validates manifest JSON. Unknown fields are
// rejected so typos fail loudly.
func ParseManifest(b []byte) (*Manifest, error) {
	m := &Manifest{}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(m); err != nil {
		return nil, xcerr.E(xcerr.CodeValidation, "invalid eval manifest JSON", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return m, nil
}

// Validate enforces the manifest contract.
func (m *Manifest) Validate() error {
	if m.Version != 1 {
		return xcerr.E(xcerr.CodeValidation,
			fmt.Sprintf("unsupported eval manifest version %d", m.Version), nil)
	}
	if len(m.Cases) == 0 {
		return xcerr.E(xcerr.CodeValidation, "eval manifest has no cases", nil)
	}
	seen := map[string]bool{}
	for i, c := range m.Cases {
		if strings.TrimSpace(c.Name) == "" {
			return xcerr.E(xcerr.CodeValidation,
				fmt.Sprintf("case %d: name must not be empty", i), nil)
		}
		if seen[c.Name] {
			return xcerr.E(xcerr.CodeValidation,
				fmt.Sprintf("case %d: duplicate name %q", i, c.Name), nil)
		}
		seen[c.Name] = true
		if strings.TrimSpace(c.Media) == "" {
			return xcerr.E(xcerr.CodeValidation,
				fmt.Sprintf("case %q: media must not be empty", c.Name), nil)
		}
		for j, r := range c.Expected {
			if !(r.Start >= 0) || math.IsNaN(r.Start) || math.IsInf(r.Start, 0) ||
				!(r.End > r.Start) || math.IsNaN(r.End) || math.IsInf(r.End, 0) {
				return xcerr.E(xcerr.CodeValidation,
					fmt.Sprintf("case %q: expected range %d must have 0 <= start < end (got %g..%g)",
						c.Name, j, r.Start, r.End), nil)
			}
		}
	}
	return nil
}
