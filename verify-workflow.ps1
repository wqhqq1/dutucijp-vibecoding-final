param([Parameter(Mandatory=$true)][string]$SourceDatabase)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'tools/execution-day.ps1')
Assert-ExecutionDay
$runtimeRoot = Join-Path $env:LOCALAPPDATA 'dutucijp-vibecoding-final'
$goExe = Join-Path $runtimeRoot 'tools/go/bin/go.exe'
$pythonExe = Join-Path $runtimeRoot 'tools/python/Scripts/python.exe'
if (-not (Test-Path -LiteralPath $goExe) -or -not (Test-Path -LiteralPath $pythonExe)) { throw 'Run ./tools/bootstrap.ps1 first' }
$sourcePath = (Resolve-Path -LiteralPath $SourceDatabase -ErrorAction Stop).Path
$sourceHash = (Get-FileHash -LiteralPath $sourcePath -Algorithm SHA256).Hash
$verificationDir = Join-Path $runtimeRoot ('verification/workflow-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '-' + [guid]::NewGuid().ToString('N').Substring(0,8))
New-Item -ItemType Directory -Path $verificationDir | Out-Null
$dbPath = Join-Path $verificationDir 'workflow.sqlite'
$binary = Join-Path $verificationDir 'recon.exe'
Push-Location $PSScriptRoot
try {
    & $pythonExe tools/copy_workflow_db.py --source $sourcePath --destination $dbPath *> (Join-Path $verificationDir 'copy.json')
    if ($LASTEXITCODE -ne 0) { throw ('Read-only workflow backup failed; see ' + $verificationDir) }
    $copy = Get-Content -LiteralPath (Join-Path $verificationDir 'copy.json') -Raw | ConvertFrom-Json
    Push-Location backend
    try { & $goExe build -o $binary ./cmd/recon; if ($LASTEXITCODE -ne 0) { throw 'Build failed' } } finally { Pop-Location }
    & $binary verify-workflow --db $dbPath --run $copy.run_id *> (Join-Path $verificationDir 'workflow.json')
    if ($LASTEXITCODE -ne 0) { throw ('Workflow verification failed; see ' + $verificationDir) }
    $result = Get-Content -LiteralPath (Join-Path $verificationDir 'workflow.json') -Raw | ConvertFrom-Json
    $sourceUnchanged = (Get-FileHash -LiteralPath $sourcePath -Algorithm SHA256).Hash -eq $sourceHash
    $receipt = [ordered]@{ actual_time=(Get-Date -Format o); source_database=$sourcePath; verification_dir=$verificationDir; source_db_sha256=$sourceHash.ToLowerInvariant(); source_database_unchanged=$sourceUnchanged; run_id=$copy.run_id; dataset_id=$copy.dataset_id; cleaning_version=$copy.cleaning_version; rule_version=$copy.rule_version; source_hash=$copy.source_hash; steps=@($result.steps).Count; steps_passed=@($result.steps | Where-Object passed -EQ $true).Count; concurrent_successes=$result.concurrent_successes; concurrent_rejections=$result.concurrent_rejections; no_new_allocations=$result.no_new_allocations; idempotent_history_preserved=$result.idempotent_history_preserved; passed=($result.passed -and $sourceUnchanged); notice=$copy.notice }
    $receipt | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $verificationDir 'summary.json') -Encoding utf8
    foreach ($step in $result.steps) { Write-Output ($step.operation + ': ' + $step.actual_code + '; state=' + $step.after.status + '; version=' + $step.after.version + '; audit=' + $step.audit_after) }
    $receipt | ConvertTo-Json -Depth 8
    if (-not $receipt.passed -or $receipt.steps -ne $receipt.steps_passed) { throw 'Workflow verification invariant failed' }
} finally { Pop-Location }
