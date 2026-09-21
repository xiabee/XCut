# Build the Windows distribution zip from the binaries in dist/.
# Usage: powershell -File scripts/make-installer.ps1 [-Version v0.1.0-alpha]
# Prereq: scripts/build-release.ps1 has produced dist/xcut-<version>-windows-amd64.exe
param(
    [string]$Version = ""
)

$ErrorActionPreference = "Stop"
Set-Location -LiteralPath (Join-Path $PSScriptRoot "..")

if (-not $Version) {
    $Version = (git describe --tags --always 2>$null)
    if (-not $Version) { $Version = "dev-$(Get-Date -Format 'yyyyMMdd-HHmm')" }
}

$exe = Join-Path "dist" "xcut-$Version-windows-amd64.exe"
if (-not (Test-Path $exe)) { throw "missing $exe — run scripts/build-release.ps1 first" }

$stage = Join-Path $env:TEMP "xcut-pkg-$([guid]::NewGuid().ToString('N').Substring(0,8))"
New-Item -ItemType Directory -Force -Path $stage | Out-Null
try {
    Copy-Item $exe (Join-Path $stage "xcut.exe")

    @"
XCut — 本地优先的自动视频剪辑 / local-first automatic video editing

【快速开始 / Quick start】
  1. 双击 xcut.exe —— 会打开图形界面窗口（浏览器方式：命令行运行 xcut serve）
  2. 需要 FFmpeg：若界面顶部出现黄色提示条，把 ffmpeg.exe 和 ffprobe.exe
     放到本目录（或 bin 子目录），重启 xcut 即自动识别。
     FFmpeg 下载：https://www.gyan.dev/ffmpeg/builds/ （选择 essentials 版）
  3. 数据默认保存在 ~/.xcut；卸载只需删除该目录与本程序文件。

【命令行 / Command line】
  xcut doctor            环境自检
  xcut auto 视频.mp4     一条龙：导入→分析→时间线→渲染
  xcut help              全部命令

【安全 / Security】
  默认只监听本机回环 (127.0.0.1)。要开放给其他机器，必须同时在配置里
  显式设 listen_remote = true 并设置访问令牌（≥24 字符），缺一则启动被拒绝。
  应用不上传数据、无遥测。会联网的只有两处，且都发生在你主动触发时：
  一键安装 FFmpeg（从 Gyan.dev 下载并做 SHA256 校验），以及你自己配置的 AI 后端。
  详见 docs/OPERATIONS.md。
"@ | Out-File -FilePath (Join-Path $stage "QUICKSTART.txt") -Encoding utf8

    Compress-Archive -Path (Join-Path $stage "*") `
        -DestinationPath (Join-Path "dist" "XCut-$Version-windows-amd64.zip") -Force
    Write-Host "packaged dist/XCut-$Version-windows-amd64.zip"
}
finally {
    Remove-Item -Recurse -Force $stage -ErrorAction SilentlyContinue
}
