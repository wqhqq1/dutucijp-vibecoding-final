$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'execution-day.ps1')
Assert-ExecutionDay
$toolLock = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'environment.lock.json') -Raw | ConvertFrom-Json
$runtimeRoot = Join-Path $env:LOCALAPPDATA 'dutucijp-vibecoding-final'
$toolRoot = Join-Path $runtimeRoot 'tools'
New-Item -ItemType Directory -Force -Path $toolRoot | Out-Null
$goExe = Join-Path $toolRoot 'go/bin/go.exe'
if (-not (Test-Path -LiteralPath $goExe)) {
    $zipPath = Join-Path $toolRoot $toolLock.go.filename
    if (-not (Test-Path -LiteralPath $zipPath)) { Invoke-WebRequest $toolLock.go.url -OutFile $zipPath }
    if ((Get-FileHash -LiteralPath $zipPath -Algorithm SHA256).Hash.ToLowerInvariant() -ne $toolLock.go.sha256) { throw 'Go archive SHA256 mismatch' }
    Expand-Archive -LiteralPath $zipPath -DestinationPath $toolRoot
    $toolLock.go | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $toolRoot 'go-download.json') -Encoding utf8
}
$goVersion = & $goExe version
Write-Output $goVersion
if ($LASTEXITCODE -ne 0 -or $goVersion -ne ('go version ' + $toolLock.go.version + ' windows/amd64')) { throw 'Go version differs from environment.lock.json' }
$pythonExe = Join-Path $toolRoot 'python/Scripts/python.exe'
if (-not (Test-Path -LiteralPath $pythonExe)) {
    $baseVersion = & python -c 'import platform; print(platform.python_version())'
    if ($LASTEXITCODE -ne 0 -or $baseVersion -ne $toolLock.python) { throw 'Install the locked Python version from python.org first' }
    & python -m venv (Join-Path $toolRoot 'python')
    if ($LASTEXITCODE -ne 0) { throw 'Python venv creation failed' }
}
$pythonVersion = & $pythonExe -c 'import platform; print(platform.python_version())'
if ($LASTEXITCODE -ne 0 -or $pythonVersion -ne $toolLock.python) { throw 'Isolated Python version mismatch' }
& $pythonExe -m pip --isolated install --no-cache-dir --no-deps --force-reinstall --require-hashes -r (Join-Path $PSScriptRoot 'requirements.txt')
if ($LASTEXITCODE -ne 0) { throw 'Python dependency installation failed' }
& $pythonExe -c "import sys,openpyxl; print(sys.version); print('openpyxl',openpyxl.__version__)"
if ($LASTEXITCODE -ne 0) { throw 'Python verification failed' }
& $pythonExe -m pip check
if ($LASTEXITCODE -ne 0) { throw 'Python dependency consistency failed' }
$previousProxy = $env:GOPROXY
$previousSumDB = $env:GOSUMDB
Push-Location (Join-Path $PSScriptRoot '../backend')
try {
    $env:GOPROXY = 'https://proxy.golang.org'
    $env:GOSUMDB = 'sum.golang.org'
    & $goExe mod download
    if ($LASTEXITCODE -ne 0) { throw 'Official Go module download failed' }
    & $goExe mod verify
    if ($LASTEXITCODE -ne 0) { throw 'Go module checksum verification failed' }
} finally {
    $env:GOPROXY = $previousProxy
    $env:GOSUMDB = $previousSumDB
    Pop-Location
}
