$ErrorActionPreference = 'Stop'
$runtimeRoot = Join-Path $env:LOCALAPPDATA 'dutucijp-vibecoding-final'
$toolRoot = Join-Path $runtimeRoot 'tools'
New-Item -ItemType Directory -Force -Path $toolRoot | Out-Null
$goExe = Join-Path $toolRoot 'go/bin/go.exe'
if (-not (Test-Path -LiteralPath $goExe)) {
    $releases = Invoke-RestMethod 'https://go.dev/dl/?mode=json'
    $release = $releases | Where-Object version -EQ 'go1.27.2'
    $archive = $release.files | Where-Object { $_.os -eq 'windows' -and $_.arch -eq 'amd64' -and $_.kind -eq 'archive' }
    if (-not $archive) { throw 'Official Go 1.27.2 Windows archive is unavailable' }
    $zipPath = Join-Path $toolRoot $archive.filename
    if (-not (Test-Path -LiteralPath $zipPath)) { Invoke-WebRequest ('https://go.dev/dl/' + $archive.filename) -OutFile $zipPath }
    if ((Get-FileHash -LiteralPath $zipPath -Algorithm SHA256).Hash.ToLowerInvariant() -ne $archive.sha256) { throw 'Go archive SHA256 mismatch' }
    Expand-Archive -LiteralPath $zipPath -DestinationPath $toolRoot
    $archive | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $toolRoot 'go-download.json') -Encoding utf8
}
& $goExe version
if ($LASTEXITCODE -ne 0) { throw 'Go verification failed' }
$pythonExe = Join-Path $toolRoot 'python/Scripts/python.exe'
if (-not (Test-Path -LiteralPath $pythonExe)) { python -m venv (Join-Path $toolRoot 'python') }
& $pythonExe -m pip install -r (Join-Path $PSScriptRoot 'requirements.txt')
if ($LASTEXITCODE -ne 0) { throw 'Python dependency installation failed' }
& $pythonExe -c "import sys,openpyxl; print(sys.version); print('openpyxl',openpyxl.__version__)"
