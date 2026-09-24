package config

// The render.encoder knob's accepted values. They live beside the Config
// they validate (config cannot import render for them — render reaches
// media which reaches config); render switches on these names when building
// ffmpeg arguments, and a render-package test cross-checks that every name
// allowed here is handled there.

// EncoderAuto probes the machine's hardware encoders at first use and falls
// back to EncoderSoftware; it is the default.
const EncoderAuto = "auto"

// EncoderSoftware is the shipped libx264 path.
const EncoderSoftware = "libx264"

// EncoderNames is the full allowlist of the render.encoder knob. A typo must
// be refused at config load, not discovered as a silent software render.
var EncoderNames = []string{
	EncoderAuto,
	EncoderSoftware,
	"h264_nvenc",
	"hevc_nvenc",
	"h264_qsv",
	"h264_amf",
	"h264_vaapi",
}

// ValidEncoderName reports whether name is a render.encoder value.
func ValidEncoderName(name string) bool {
	for _, n := range EncoderNames {
		if n == name {
			return true
		}
	}
	return false
}
