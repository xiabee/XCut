package config

// MergeLayer overlays non-zero values from layer onto base and returns a new
// config (used for two-layer loading: bootstrap config + workspace config).
// A field set in the layer wins; a zero value keeps the base value. A tri-state
// knob (*bool) is carried when the layer's pointer is non-nil, which is the only
// reading that says "the file chose this"; slices and maps are replaced, never
// merged (none exist today).
func MergeLayer(base, layer *Config) *Config {
	out := *base

	if layer.Workspace != "" {
		out.Workspace = layer.Workspace
	}
	if layer.Log.Level != "" {
		out.Log.Level = layer.Log.Level
	}
	if layer.Log.MaxSizeMB != 0 {
		out.Log.MaxSizeMB = layer.Log.MaxSizeMB
	}
	if layer.Log.MaxFiles != 0 {
		out.Log.MaxFiles = layer.Log.MaxFiles
	}
	if layer.Server.Listen != "" {
		out.Server.Listen = layer.Server.Listen
	}
	if layer.Server.ListenRemote {
		out.Server.ListenRemote = true
	}
	if layer.Server.AuthToken != "" {
		out.Server.AuthToken = layer.Server.AuthToken
	}
	if layer.Resource.MaxConcurrentJobs != 0 {
		out.Resource.MaxConcurrentJobs = layer.Resource.MaxConcurrentJobs
	}
	if layer.Resource.MaxFFmpegProcesses != 0 {
		out.Resource.MaxFFmpegProcesses = layer.Resource.MaxFFmpegProcesses
	}
	if layer.Resource.MaxAnalysisWorkers != 0 {
		out.Resource.MaxAnalysisWorkers = layer.Resource.MaxAnalysisWorkers
	}
	if layer.Resource.MaxRenderWorkers != 0 {
		out.Resource.MaxRenderWorkers = layer.Resource.MaxRenderWorkers
	}
	if layer.Resource.FFmpegThreads != 0 {
		out.Resource.FFmpegThreads = layer.Resource.FFmpegThreads
	}
	if layer.Resource.FFmpegMaxMemoryMB != 0 {
		out.Resource.FFmpegMaxMemoryMB = layer.Resource.FFmpegMaxMemoryMB
	}
	if layer.Resource.MaxCacheGB != 0 {
		out.Resource.MaxCacheGB = layer.Resource.MaxCacheGB
	}
	if layer.Resource.MaxTempGB != 0 {
		out.Resource.MaxTempGB = layer.Resource.MaxTempGB
	}
	if layer.Resource.FrameSampleFPS != 0 {
		out.Resource.FrameSampleFPS = layer.Resource.FrameSampleFPS
	}
	if layer.Resource.AnalysisWidth != 0 {
		out.Resource.AnalysisWidth = layer.Resource.AnalysisWidth
	}
	if layer.Resource.ProxyThreads != 0 {
		out.Resource.ProxyThreads = layer.Resource.ProxyThreads
	}
	if layer.Resource.MaxProxyGB != 0 {
		out.Resource.MaxProxyGB = layer.Resource.MaxProxyGB
	}
	if layer.Resource.AnalyzerCallTimeout.Duration != 0 {
		out.Resource.AnalyzerCallTimeout = layer.Resource.AnalyzerCallTimeout
	}
	// A pointer so the layer can say "off" and mean it: nil is the only reading
	// that keeps the base. (With a bool, an explicit false and an absent key were
	// the same bytes, and a workspace config could opt proxies in but never out.)
	if layer.Resource.ProxyEnabled != nil {
		out.Resource.ProxyEnabled = layer.Resource.ProxyEnabled
	}
	if layer.FFmpeg.Bin != "" {
		out.FFmpeg.Bin = layer.FFmpeg.Bin
	}
	if layer.FFmpeg.ProbeBin != "" {
		out.FFmpeg.ProbeBin = layer.FFmpeg.ProbeBin
	}
	if layer.Job.StaleRunningAfter.Duration != 0 {
		out.Job.StaleRunningAfter = layer.Job.StaleRunningAfter
	}
	if layer.Job.MaxHistory != 0 {
		out.Job.MaxHistory = layer.Job.MaxHistory
	}
	if layer.Render.Encoder != "" {
		out.Render.Encoder = layer.Render.Encoder
	}
	if layer.Workers.MediaBin != "" {
		out.Workers.MediaBin = layer.Workers.MediaBin
	}
	if layer.Workers.Audio != "" {
		out.Workers.Audio = layer.Workers.Audio
	}
	if layer.Workers.AIBin != "" {
		out.Workers.AIBin = layer.Workers.AIBin
	}
	return &out
}
