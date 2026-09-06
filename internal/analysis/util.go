package analysis

// tailBytes returns the last 400 bytes of output for error messages (bounded
// so a pathological ffmpeg error dump cannot blow up logs).
func tailBytes(b []byte) string {
	if len(b) > 400 {
		return string(b[len(b)-400:])
	}
	return string(b)
}
