package config

// MergeLayer overlays non-zero values from layer onto base and returns a new
// config (used for two-layer loading: bootstrap config + workspace config).
// A field set in the layer wins; a zero value keeps the base value. Slices
// and maps are replaced, never merged (none exist today).
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
	// A bool cannot express "unset", so a workspace may opt proxies IN but
	// not out — turning them off is the bootstrap config / XCUT_PROXY_ENABLED
	// / flag layer's job.
	if layer.Resource.ProxyEnabled {
		out.Resource.ProxyEnabled = true
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
