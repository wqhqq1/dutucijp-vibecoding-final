param([ValidateSet('benchmark','year1','all')][string]$Scope = 'year1')
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'tools/execution-day.ps1')
Assert-ExecutionDay
$runtimeRoot = Join-Path $env:LOCALAPPDATA 'dutucijp-vibecoding-final'
$goExe = Join-Path $runtimeRoot 'tools/go/bin/go.exe'
$pythonExe = Join-Path $runtimeRoot 'tools/python/Scripts/python.exe'
if (-not (Test-Path -LiteralPath $goExe) -or -not (Test-Path -LiteralPath $pythonExe)) { throw 'Run ./tools/bootstrap.ps1 first' }
$verificationDir = Join-Path $runtimeRoot ('verification/' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '-' + [guid]::NewGuid().ToString('N').Substring(0,8))
New-Item -ItemType Directory -Path $verificationDir | Out-Null
Push-Location $PSScriptRoot
try {
    & $pythonExe tools/fetch_data.py *> (Join-Path $verificationDir 'source.txt')
    if ($LASTEXITCODE -ne 0) { throw 'Official source acquisition failed' }
    & $pythonExe tools/prepare.py --scope $Scope *> (Join-Path $verificationDir 'prepare.jsonl')
    if ($LASTEXITCODE -ne 0) { throw 'Preparation failed; unpublished preparation kept for diagnosis' }
    $prepared = (Get-Content -LiteralPath (Join-Path $verificationDir 'prepare.jsonl') -Tail 1 | ConvertFrom-Json).path
    $binary = Join-Path $verificationDir 'recon.exe'
    Push-Location backend
    try { & $goExe build -o $binary ./cmd/recon; if ($LASTEXITCODE -ne 0) { throw 'Build failed' } } finally { Pop-Location }
    $dbPath = Join-Path $verificationDir 'verification.sqlite'
    & $binary run --prepared $prepared --db $dbPath *> (Join-Path $verificationDir 'run.json')
    if ($LASTEXITCODE -ne 0) { throw 'Import or reconciliation failed; no published success' }
    $run = Get-Content -LiteralPath (Join-Path $verificationDir 'run.json') -Raw | ConvertFrom-Json
    & $binary run --prepared $prepared --db $dbPath *> (Join-Path $verificationDir 'rerun.json')
    if ($LASTEXITCODE -ne 0) { throw 'Idempotency rerun failed' }
    & $binary examples --db $dbPath --run $run.run_id *> (Join-Path $verificationDir 'official-examples.json')
    if ($LASTEXITCODE -ne 0) { throw 'Evidence lookup failed' }
    Get-Content -LiteralPath (Join-Path $verificationDir 'run.json')
    Write-Output ('Original outputs and private official row evidence: ' + $verificationDir)
    Write-Output 'This is an isolated backend verification, not a browser workflow, human operation, or NFR acceptance.'
} finally { Pop-Location }
