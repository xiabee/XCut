package xcerr

import (
	"errors"
	"fmt"
	"testing"
)

func TestCodeOf(t *testing.T) {
	base := errors.New("disk on fire")
	wrapped := fmt.Errorf("save failed: %w", E(CodeStorageFailure, "cannot save project", base))

	if got := CodeOf(wrapped); got != CodeStorageFailure {
		t.Fatalf("CodeOf = %s", got)
	}
	if got := CodeOf(base); got != CodeInternal {
		t.Fatalf("CodeOf(plain) = %s, want internal", got)
	}
	if !errors.Is(wrapped, base) {
		t.Fatal("cause chain broken")
	}
}

func TestUserMessageHidesCause(t *testing.T) {
	err := E(CodeUnsupportedMedia, "file is not a supported video", errors.New("moov atom not found"))
	if got := UserMessage(err); got != "file is not a supported video" {
		t.Fatalf("UserMessage = %q", got)
	}
	if got := UserMessage(errors.New("raw")); got != "internal error" {
		t.Fatalf("UserMessage(raw) = %q", got)
	}
}
