# GitSwitch one-line installer for Windows (PowerShell 5.1 or 7+):
#   irm https://raw.githubusercontent.com/macdipu/gitswitch/main/install.ps1 | iex
# Options: $env:GITSWITCH_VERSION = 'v1.2.0'   $env:GITSWITCH_REPO = 'owner/repo'
#          $env:GITSWITCH_CLI_ONLY = '1'  (terminal programs only, no desktop app)
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
$asset    = "gitswitch-windows-$cpu.exe"     # git hook, CLI and terminal app
$appAsset = "gitswitch-app-windows-$cpu.exe" # desktop app

if ($env:GITSWITCH_BASE_URL) { $base = $env:GITSWITCH_BASE_URL }
elseif ($version -eq 'latest') { $base = "https://github.com/$repo/releases/latest/download" }
else { $base = "https://github.com/$repo/releases/download/$version" }

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("gitswitch-" + [Guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  $sums = Join-Path $tmp 'SHA256SUMS'
  Invoke-WebRequest -UseBasicParsing -Uri "$base/SHA256SUMS" -OutFile $sums

  # Downloads one file as $name and checks it. Returns $false when the
  # release has no such file (an older release).
  function Get-Verified($file, $name) {
    $line = Get-Content $sums | Where-Object { $_ -match "\s\*?$([regex]::Escape($file))$" } | Select-Object -First 1
    if (-not $line) { return $false }
    $out = Join-Path $tmp $name
    Write-Host "Downloading $file ($version)..."
    Invoke-WebRequest -UseBasicParsing -Uri "$base/$file" -OutFile $out
    $expected = ($line -split '\s+')[0].ToLower()
    $actual   = (Get-FileHash -Algorithm SHA256 $out).Hash.ToLower()
    if ($expected -ne $actual) { throw "Checksum mismatch for $file. Nothing was installed." }
    Write-Host 'Checksum OK.'
    return $true
  }

  # Both programs go in one folder: gitswitch install finds gitswitch-app.exe
  # next to itself and installs it too.
  if (-not (Get-Verified $asset 'gitswitch.exe')) { throw "No checksum for $asset in SHA256SUMS" }
  $exe = Join-Path $tmp 'gitswitch.exe'
  $haveApp = $false
  if (-not $env:GITSWITCH_CLI_ONLY) {
    $haveApp = Get-Verified $appAsset 'gitswitch-app.exe'
    if (-not $haveApp) { Write-Host 'This release has no separate desktop app; installing the terminal programs.' }
  }

  # Make sure it starts here before git depends on it (git runs it on every
  # clone, pull and push).
  & $exe version | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "$asset does not start on this system. Nothing was installed." }

  # Copies the programs to %LOCALAPPDATA%\GitSwitch, hooks into git, adds
  # gitswitch to PATH and the desktop app to the Start menu.
  & $exe install
  if ($LASTEXITCODE -ne 0) { throw "gitswitch install failed" }

  Write-Host ''
  Write-Host 'Done. Open a new terminal, then run:' -ForegroundColor Green
  Write-Host '  gitswitch        terminal app'
  if ($haveApp) { Write-Host '  gitswitch ui     desktop app (or GitSwitch in the Start menu)' }
  Write-Host '  gitswitch setup  step-by-step setup'
}
finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
