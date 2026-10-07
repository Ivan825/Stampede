# Installs the stampede CLI on Windows (one binary: CLI, server, worker and
# console).
#
#   irm https://raw.githubusercontent.com/Ivan825/Stampede/main/install.ps1 | iex
#
# It downloads the latest release for this CPU from GitHub, checks it
# against the release's SHA-256 checksums, puts it in
# %LOCALAPPDATA%\stampede\bin and adds that folder to your user PATH.
#
# Settings (environment variables):
#   STAMPEDE_VERSION        a release tag such as v1.0.0 (default: latest)
#   STAMPEDE_INSTALL_DIR    where to put stampede.exe
#   STAMPEDE_DOWNLOAD_BASE  download from this URL instead of the GitHub
#                           release (a mirror; it must hold the same files)
$ErrorActionPreference = 'Stop'
$repo = 'Ivan825/Stampede'

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  'AMD64' { 'amd64' }
  'ARM64' { 'arm64' }
  default { throw "unsupported CPU $($env:PROCESSOR_ARCHITECTURE); build from source with: go install github.com/$repo/cmd/stampede@latest" }
}
$dir = if ($env:STAMPEDE_INSTALL_DIR) { $env:STAMPEDE_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'stampede\bin' }
New-Item -ItemType Directory -Force -Path $dir | Out-Null

$version = $env:STAMPEDE_VERSION
if (-not $version -and -not $env:STAMPEDE_DOWNLOAD_BASE) {
  try {
    $version = (Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest").tag_name
  } catch {
    $version = $null
  }
}

if (-not $version) {
  if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw 'no release is published yet and Go is not installed. Install Go (https://go.dev/dl/) and run this again, or use Docker Compose (see the README).'
  }
  Write-Host 'No release is published yet: building from source with Go (this takes a minute).'
  $env:GOBIN = $dir
  $env:GOTOOLCHAIN = 'auto'
  go install "github.com/$repo/cmd/stampede@latest"
  if ($LASTEXITCODE -ne 0) { throw 'go install failed' }
} else {
  $v = $version.TrimStart('v')
  $base = if ($env:STAMPEDE_DOWNLOAD_BASE) { $env:STAMPEDE_DOWNLOAD_BASE } else { "https://github.com/$repo/releases/download/$version" }
  $asset = "stampede_${v}_windows_${arch}.zip"
  $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("stampede-" + [guid]::NewGuid())
  New-Item -ItemType Directory -Path $tmp | Out-Null
  try {
    Write-Host "Downloading stampede $version for windows/$arch"
    Invoke-WebRequest -UseBasicParsing "$base/$asset" -OutFile (Join-Path $tmp $asset)
    Invoke-WebRequest -UseBasicParsing "$base/stampede_${v}_checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt')
    $want = (Get-Content (Join-Path $tmp 'checksums.txt') | Where-Object { ($_ -split '\s+')[1] -eq $asset } | ForEach-Object { ($_ -split '\s+')[0] }) | Select-Object -First 1
    $got = (Get-FileHash -Algorithm SHA256 (Join-Path $tmp $asset)).Hash.ToLower()
    if (-not $want -or $want -ne $got) { throw "checksum mismatch for $asset (want $want, got $got)" }
    Expand-Archive -Force (Join-Path $tmp $asset) -DestinationPath (Join-Path $tmp 'x')
    Copy-Item -Force (Join-Path $tmp 'x\stampede.exe') (Join-Path $dir 'stampede.exe')
  } finally {
    Remove-Item -Recurse -Force $tmp
  }
}

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($userPath -split ';') -notcontains $dir) {
  [Environment]::SetEnvironmentVariable('Path', "$userPath;$dir", 'User')
  $env:Path = "$env:Path;$dir"
  Write-Host "Added $dir to your PATH (open a new terminal for it to apply everywhere)."
}
$installed = & (Join-Path $dir 'stampede.exe') version | Select-Object -First 1
Write-Host "Installed $installed to $dir\stampede.exe"
Write-Host ''
Write-Host 'Next:'
Write-Host '  stampede init --target http://localhost:3000   # detect your app, install a pack, dry-run it'
Write-Host '  stampede                                        # the interactive console'
Write-Host '  stampede up                                     # the full stack with the web UI (needs Docker)'
