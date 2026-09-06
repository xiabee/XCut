package style

import (
	"embed"
	"strings"
)

// Every JSON file in presets/ is embedded automatically — adding a preset is
// just adding a file (it still has to pass schema validation, which tests
// and Load enforce at use time).
//
//go:embed presets/*.json
var presetFS embed.FS

var embedded = func() map[string][]byte {
	m := map[string][]byte{}
	entries, err := presetFS.ReadDir("presets")
	if err != nil {
		panic("style: broken embedded presets: " + err.Error())
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := presetFS.ReadFile("presets/" + e.Name())
		if err != nil {
			panic("style: cannot read embedded preset " + e.Name() + ": " + err.Error())
		}
		m[strings.TrimSuffix(e.Name(), ".json")] = b
	}
	return m
}()
