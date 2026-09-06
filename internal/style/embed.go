package style

import _ "embed"

//go:embed presets/generic_highlight.json
var genericHighlight []byte

//go:embed presets/badminton_highlight.json
var badmintonHighlight []byte

var embedded = map[string][]byte{
	"generic_highlight":   genericHighlight,
	"badminton_highlight": badmintonHighlight,
}
