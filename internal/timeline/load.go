package timeline

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/xiabee/XCut/internal/xcerr"
)

// maxTimelineBytes caps timeline file size (a timeline is <<1 MB by design).
const maxTimelineBytes = 8 << 20

// LoadFile reads, parses and structurally validates a timeline document.
// Content checks that need media metadata still run via Validate(lookup).
func LoadFile(path string) (*Timeline, error) {
	fi, err := os.Stat(path)
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
	b, err := os.ReadFile(path)
	if err != nil {
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
