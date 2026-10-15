param([ValidateSet('benchmark','year1','all')][string]$Scope = 'year1')
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'tools/execution-day.ps1')
Assert-ExecutionDay
$runtimeRoot = Join-Path $env:LOCALAPPDATA 'dutucijp-vibecoding-final'
$goExe = Join-Path $runtimeRoot 'tools/go/bin/go.exe'
$pythonExe = Join-Path $runtimeRoot 'tools/python/Scripts/python.exe'
if (-not (Test-Path -LiteralPath $goExe) -or -not (Test-Path -LiteralPath $pythonExe)) { throw 'Run ./tools/bootstrap.ps1 first' }
$verificationDir = Join-Path $runtimeRoot ('verification/data-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '-' + [guid]::NewGuid().ToString('N').Substring(0,8))
New-Item -ItemType Directory -Path $verificationDir | Out-Null
Push-Location $PSScriptRoot
try {
    & $pythonExe tools/fetch_data.py *> (Join-Path $verificationDir 'source.txt')
    if ($LASTEXITCODE -ne 0) { throw 'Official source verification failed; see source.txt' }
    # A fresh preparation directory forces an actual reread, rather than a cache hit.
    & $pythonExe tools/prepare.py --scope $Scope --output (Join-Path $verificationDir 'prepared') *> (Join-Path $verificationDir 'prepare.jsonl')
    if ($LASTEXITCODE -ne 0) { throw 'Preparation failed; original outputs retained' }
    $prepared = (Get-Content -LiteralPath (Join-Path $verificationDir 'prepare.jsonl') -Tail 1 | ConvertFrom-Json).path
    $binary = Join-Path $verificationDir 'recon.exe'
    Push-Location backend
    try { & $goExe build -o $binary ./cmd/recon; if ($LASTEXITCODE -ne 0) { throw 'Build failed' } } finally { Pop-Location }
    $database = Join-Path $verificationDir 'verification.sqlite'
    & $binary verify --prepared $prepared --db $database *> (Join-Path $verificationDir 'verify.json')
    if ($LASTEXITCODE -ne 0) { throw 'SQLite import/conservation failed; see verify.json' }
    $result = Get-Content -LiteralPath (Join-Path $verificationDir 'verify.json') -Raw | ConvertFrom-Json
    $sourcePath = Join-Path $runtimeRoot 'raw/online_retail_II.xlsx'
    $afterHash = (Get-FileHash -LiteralPath $sourcePath).Hash.ToLowerInvariant()
    $readOnly = (Get-Item -LiteralPath $sourcePath).IsReadOnly
    if ($afterHash -ne $result.source_hash -or -not $readOnly) { throw 'Original SHA/read-only verification failed' }
    $example = $result.excluded_source_example
    $publicExample = if ($null -ne $example) { [ordered]@{id=$example.id; sheet=$example.sheet; source_row=$example.source_row; reason=$example.excluded; flags=$example.flags; duplicate_of=$example.duplicate_of} } else { $null }
    $summary = [ordered]@{actual_time=([DateTimeOffset]::UtcNow.ToOffset([TimeSpan]::FromHours(8)).ToString('o')); scope=$Scope; dataset_id=$result.dataset_id; cleaning_version=$result.cleaning_version; go_sqlite_version=$result.go_sqlite_version; source_hash=$afterHash; source_read_only=$readOnly; source=$result.source; counts=$result.counts; sheets=$result.sheets; exclusion_reasons=$result.exclusion_reasons; conserved=$result.conserved; excluded_source_location=$publicExample; private_output_dir=$verificationDir; database=$database; prepared=$prepared; notice='Isolated FR-01..04 CLI verification. Full raw fields are only in private verify.json; no HTTP service or business login. This is not NFR acceptance.'}
    $summary | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $verificationDir 'summary.json') -Encoding utf8
    Get-Content -LiteralPath (Join-Path $verificationDir 'summary.json')
} finally { Pop-Location }
