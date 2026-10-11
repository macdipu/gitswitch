# GitSwitch one-line installer for Windows (PowerShell 5.1 or 7+):
#   irm https://raw.githubusercontent.com/macdipu/gitswitch/main/install.ps1 | iex
# Options: $env:GITSWITCH_VERSION = 'v1.2.0'   $env:GITSWITCH_REPO = 'owner/repo'
$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

$repo    = if ($env:GITSWITCH_REPO)    { $env:GITSWITCH_REPO }    else { 'macdipu/gitswitch' }
$version = if ($env:GITSWITCH_VERSION) { $env:GITSWITCH_VERSION } else { 'latest' }

if (-not (Get-Command git -ErrorAction SilentlyContinue)) {
  Write-Host 'Git is not installed. Install Git for Windows (https://git-scm.com), then run this again.' -ForegroundColor Red
  return
}

$arch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
switch ($arch) {
  'AMD64' { $cpu = 'amd64' }
  'ARM64' { $cpu = 'arm64' }
  default { Write-Host "Unsupported CPU: $arch" -ForegroundColor Red; return }
}
$asset = "gitswitch-windows-$cpu.exe"

if ($env:GITSWITCH_BASE_URL) { $base = $env:GITSWITCH_BASE_URL }
elseif ($version -eq 'latest') { $base = "https://github.com/$repo/releases/latest/download" }
else { $base = "https://github.com/$repo/releases/download/$version" }

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("gitswitch-" + [Guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  $exe  = Join-Path $tmp $asset
  $sums = Join-Path $tmp 'SHA256SUMS'
  Write-Host "Downloading $asset ($version)..."
  Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset" -OutFile $exe
  Invoke-WebRequest -UseBasicParsing -Uri "$base/SHA256SUMS" -OutFile $sums

  $line = Get-Content $sums | Where-Object { $_ -match "\s\*?$([regex]::Escape($asset))$" } | Select-Object -First 1
  if (-not $line) { throw "No checksum for $asset in SHA256SUMS" }
  $expected = ($line -split '\s+')[0].ToLower()
  $actual   = (Get-FileHash -Algorithm SHA256 $exe).Hash.ToLower()
  if ($expected -ne $actual) { throw "Checksum mismatch for $asset. Nothing was installed." }
  Write-Host 'Checksum OK.'

  # The program copies itself to %LOCALAPPDATA%\GitSwitch, hooks into git,
  # adds itself to PATH and to the Start menu.
  & $exe install
  if ($LASTEXITCODE -ne 0) { throw "gitswitch install failed" }

  Write-Host ''
  Write-Host 'Done. Open a new terminal, then run:' -ForegroundColor Green
  Write-Host '  gitswitch        terminal app'
  Write-Host '  gitswitch ui     desktop app (or GitSwitch in the Start menu)'
  Write-Host '  gitswitch setup  step-by-step setup'
}
finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
