package api

import (
	"encoding/json"

	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/timeline"
)

func jsonMarshalTL(tl *timeline.Timeline) ([]byte, error) {
	return json.Marshal(tl)
}

func storageAssetFor(projectID string) storage.Asset {
	return storage.Asset{
		ProjectID:   projectID,
		Path:        "src.mp4",
		Filename:    "src.mp4",
		Fingerprint: "fp",
		DurationSec: 10,
		Width:       640,
		Height:      360,
		FPS:         30,
		VideoCodec:  "h264",
		HasAudio:    true,
		AudioCodec:  "aac",
	}
}
