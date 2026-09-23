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
			ProxyEnabled:        boolPtr(true),
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
	if got.ProxyOn() != true {
		t.Fatal("workspace proxy_enabled=true must opt in")
	}
	// The field is a pointer because a bool cannot express "unset". A layer that
	// says nothing leaves the base alone; a layer that says false means it.
	if !MergeLayer(Default(), &Config{}).ProxyOn() {
		t.Fatal("an unmentioned proxy_enabled must keep the base value")
	}
	off := false
	if MergeLayer(Default(), &Config{Resource: Resource{ProxyEnabled: &off}}).ProxyOn() {
		t.Fatal("an explicit false in the layer must override the shipped default — that " +
			"override is the whole reason the field is not a bool")
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
	case reflect.Ptr:
		// The tri-state knobs are pointers (a bool cannot say "unsaid"), and a
		// merge line that forgot one would otherwise be invisible to this probe.
		// Only *bool is supported here on purpose: a new pointer kind must arrive
		// with a comment on what "set" means for it, so it panics until then.
		if v.Type().Elem().Kind() != reflect.Bool {
			panic("merge completeness probe: pointer field of non-bool kind at " + v.Type().String())
		}
		on := true
		v.Set(reflect.ValueOf(&on))
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
	case reflect.Ptr:
		if layer.IsNil() {
			panic("merge completeness probe: pointer leaf was not filled in the layer at " + path)
		}
		if out.IsNil() || out.Elem().Bool() != layer.Elem().Bool() {
			missing = append(missing, path+" (*bool)")
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
