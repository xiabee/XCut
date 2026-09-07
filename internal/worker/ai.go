package worker

import (
	"context"
	"encoding/json"
	"os/exec"
	"time"

	"github.com/xiabee/XCut/internal/xcerr"
)

// AI sidecar protocol v1 (DECISIONS D3): the same stdin/stdout JSON envelope
// as the Rust media worker, extended with capability discovery and health so
// the core can degrade gracefully no matter which models a sidecar offers.
//
// Ops (all optional for a sidecar except describe):
//
//	describe     → Describe (name, version, protocol, ops)
//	capabilities → AICapabilities (ops with descriptions, model availability)
//	health       → AIHealth (ready, loaded models)
//	analyze      → op-specific result (params carry the request)
//
// Never a core dependency: a missing sidecar is a capability gap, not an
// error path the pipeline depends on.

// DefaultAIBin is the PATH name a sidecar binary/script is expected to have.
const DefaultAIBin = "xcut-ai"

// MaxAIResponseBytes caps one sidecar response (a worker must never be able
// to OOM the host through its stdout; ARCHITECTURE.md worker contract).
const MaxAIResponseBytes = 32 << 20

// DefaultAIAnalyzeTimeout bounds one analyze call (model inference can be
// slow; this is a runaway guard, not an interactive deadline).
const DefaultAIAnalyzeTimeout = 10 * time.Minute

// AIOp is one operation the sidecar advertises.
type AIOp struct {
	Op   string `json:"op"`
	Desc string `json:"desc,omitempty"`
}

// AIModel is one advertised model and whether it is locally usable.
type AIModel struct {
	Name      string `json:"name"`
	Available bool   `json:"available"` // installed and loadable on this machine
	Loaded    bool   `json:"loaded"`    // resident in memory right now
	Detail    string `json:"detail,omitempty"`
}

// AICapabilities is the `capabilities` result.
type AICapabilities struct {
	Ops    []AIOp            `json:"ops"`
	Models []AIModel         `json:"models,omitempty"`
	Device string            `json:"device,omitempty"` // cpu | cuda | mps | …
	Extra  map[string]string `json:"extra,omitempty"`
}

// AIHealth is the `health` result.
type AIHealth struct {
	Ready        bool     `json:"ready"`
	ModelsLoaded []string `json:"models_loaded,omitempty"`
	Detail       string   `json:"detail,omitempty"`
}

// ResolveAIBin returns the configured AI sidecar binary or PATH lookups of
// the default names. Returns "" when no sidecar is available.
func ResolveAIBin(configured string) string {
	if configured != "" {
		if p, err := exec.LookPath(configured); err == nil {
			return p
		}
		return configured // explicit config wins; the call will surface errors
	}
	for _, name := range []string{DefaultAIBin, "xcut-ai-sidecar.py"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// Capabilities runs the `capabilities` op.
func Capabilities(ctx context.Context, bin string) (*AICapabilities, error) {
	raw, err := Call(ctx, bin, Request{Protocol: Protocol, Op: "capabilities"})
	if err != nil {
		return nil, err
	}
	var c AICapabilities
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, xcerr.E(xcerr.CodeInternal, "sidecar capabilities unparseable", err)
	}
	return &c, nil
}

// Health runs the `health` op.
func Health(ctx context.Context, bin string) (*AIHealth, error) {
	raw, err := Call(ctx, bin, Request{Protocol: Protocol, Op: "health"})
	if err != nil {
		return nil, err
	}
	var h AIHealth
	if err := json.Unmarshal(raw, &h); err != nil {
		return nil, xcerr.E(xcerr.CodeInternal, "sidecar health unparseable", err)
	}
	return &h, nil
}

// AIAnalyze runs an analyze-style op against the sidecar with an explicit
// timeout and a hard response-size cap (worker output security: the sidecar
// can never grow host memory without bound through its stdout).
func AIAnalyze(ctx context.Context, bin, op, input string, params map[string]any, timeout time.Duration) (json.RawMessage, error) {
	if timeout <= 0 {
		timeout = DefaultAIAnalyzeTimeout
	}
	return callBounded(ctx, bin, Request{
		Protocol: Protocol,
		Op:       op,
		Input:    input,
		Params:   params,
	}, timeout, MaxAIResponseBytes)
}
