package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMergeLayerOverridesAndKeeps(t *testing.T) {
	base := Default()
	_ = Resolve(base)

	layer := &Config{
		Resource: Resource{MaxConcurrentJobs: 5, FrameSampleFPS: 4},
		Workers:  Workers{Audio: "rust", AIBin: "C:/sidecars/fake-ai.py"},
		Log:      Log{Level: "debug"},
	}
	got := MergeLayer(base, layer)

	if got.Resource.MaxConcurrentJobs != 5 {
		t.Fatalf("layered jobs = %d", got.Resource.MaxConcurrentJobs)
	}
	if got.Resource.FrameSampleFPS != 4 {
		t.Fatalf("layered fps = %v", got.Resource.FrameSampleFPS)
	}
	if got.Workers.Audio != "rust" {
		t.Fatalf("layered audio = %q", got.Workers.Audio)
	}
	if got.Workers.AIBin != "C:/sidecars/fake-ai.py" {
		t.Fatalf("layered ai_bin = %q (workspace workers.ai_bin must not be dropped)", got.Workers.AIBin)
	}
	if got.Log.Level != "debug" {
		t.Fatalf("layered log level = %q", got.Log.Level)
	}
	// Untouched fields keep base values.
	if got.Resource.MaxCacheGB != base.Resource.MaxCacheGB {
		t.Fatalf("cache budget changed: %v", got.Resource.MaxCacheGB)
	}
	if got.Server.Listen != base.Server.Listen {
		t.Fatalf("listen changed: %v", got.Server.Listen)
	}
	// Base must not be mutated.
	if base.Resource.MaxConcurrentJobs == 5 {
		t.Fatal("base config mutated by merge")
	}
}

// TestMergeLayerCarriesNewerResourceKnobs: the proxy/timeout knobs postdate
// the merge function and were silently dropped from the workspace layer —
// config show shows the merged result, so nothing looked wrong.
func TestMergeLayerCarriesNewerResourceKnobs(t *testing.T) {
	base := Default()
	_ = Resolve(base)

	d := Duration{5 * time.Minute}
	layer := &Config{
		Resource: Resource{
			ProxyThreads:        3,
			MaxProxyGB:          4,
			AnalyzerCallTimeout: d,
			ProxyEnabled:        true,
		},
	}
	got := MergeLayer(base, layer)
	if got.Resource.ProxyThreads != 3 {
		t.Fatalf("layered proxy_threads = %d", got.Resource.ProxyThreads)
	}
	if got.Resource.MaxProxyGB != 4 {
		t.Fatalf("layered max_proxy_gb = %v", got.Resource.MaxProxyGB)
	}
	if got.Resource.AnalyzerCallTimeout.Duration != 5*time.Minute {
		t.Fatalf("layered analyzer_call_timeout = %v", got.Resource.AnalyzerCallTimeout.Duration)
	}
	if !got.Resource.ProxyEnabled {
		t.Fatal("workspace proxy_enabled=true must opt in")
	}
	// The bool cannot express "unset": a false layer keeps the base value
	// (turning it off is the bootstrap/env/flag layer's job).
	base2 := Default()
	base2.Resource.ProxyEnabled = true
	got2 := MergeLayer(base2, &Config{})
	if !got2.Resource.ProxyEnabled {
		t.Fatal("false in the layer must keep the base value")
	}
}

// MergeLayer is a hand-written field walk, so a field added to Config without
// a matching merge line is silently dropped from the workspace layer —
// exactly what shipped with resource.ffmpeg_max_memory_mb: the cap was set in
// the workspace config and config show still reported 0. These tests walk
// every leaf of the struct with reflection so the NEXT added field fails
// here instead of disappearing for users. Reflection pins completeness, not
// per-field policy — the intent-specific assertions above stay.

const (
	probeString = "set-by-layer"
	probeInt    = 7
	probeFloat  = 7.5
	probeNanos  = int64(7 * time.Second)
)

func setNonZero(v reflect.Value) {
	switch v.Kind() {
	case reflect.String:
		v.SetString(probeString)
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int32, reflect.Int64:
		v.SetInt(probeNanos)
	case reflect.Float64:
		v.SetFloat(probeFloat)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() {
				setNonZero(v.Field(i))
			}
		}
	default:
		panic("merge completeness probe: unhandled kind " + v.Kind().String())
	}
}

func droppedLeaves(layer, out reflect.Value, path string) []string {
	var missing []string
	switch layer.Kind() {
	case reflect.Struct:
		for i := 0; i < layer.NumField(); i++ {
			name := path + "." + layer.Type().Field(i).Name
			missing = append(missing, droppedLeaves(layer.Field(i), out.Field(i), name)...)
		}
	case reflect.String:
		if out.String() != layer.String() {
			missing = append(missing, path+" (string)")
		}
	case reflect.Bool:
		if out.Bool() != layer.Bool() {
			missing = append(missing, path+" (bool)")
		}
	case reflect.Int, reflect.Int32, reflect.Int64:
		if out.Int() != layer.Int() {
			missing = append(missing, path+" (int)")
		}
	case reflect.Float64:
		if out.Float() != layer.Float() {
			missing = append(missing, path+" (float)")
		}
	default:
		panic("merge completeness probe: unhandled kind " + layer.Kind().String() + " at " + path)
	}
	return missing
}

func TestMergeLayerCarriesEveryLeafField(t *testing.T) {
	layer := &Config{}
	setNonZero(reflect.ValueOf(layer).Elem())

	out := MergeLayer(&Config{}, layer)
	missing := droppedLeaves(reflect.ValueOf(layer).Elem(), reflect.ValueOf(out).Elem(), "Config")
	if len(missing) > 0 {
		t.Fatalf("MergeLayer dropped layer fields (add a merge line for each): %s", strings.Join(missing, ", "))
	}
}

func TestMergeLayerKeepsBaseWhenLayerZero(t *testing.T) {
	base := Default()
	out := MergeLayer(base, &Config{})
	if !reflect.DeepEqual(out, base) {
		t.Fatal("a zero layer must not change the base config")
	}
}
