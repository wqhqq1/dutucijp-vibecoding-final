param([string]$Database='', [switch]$CheckOnly)
$ErrorActionPreference='Stop'
$projectRoot=Split-Path $PSScriptRoot -Parent
$runtimeRoot=Join-Path $env:LOCALAPPDATA 'dutucijp-vibecoding-final'
$build=Get-Content -LiteralPath (Join-Path $runtimeRoot 'active-build.json') -Raw | ConvertFrom-Json
if(-not (Test-Path -LiteralPath $build.binary) -or (Get-FileHash -LiteralPath $build.binary -Algorithm SHA256).Hash.ToLowerInvariant() -ne $build.sha256){throw 'The selected validated build is missing or changed. Build and validate before selecting it.'}
if(-not $Database){
    $candidates=@(Get-ChildItem -LiteralPath (Join-Path $runtimeRoot 'demos') -Directory | Where-Object {Test-Path -LiteralPath (Join-Path $_.FullName 'business.sqlite')})
    if($candidates.Count -ne 1){throw 'Specify -Database explicitly when there is not exactly one existing personal demo. No state was changed.'}
    $Database=Join-Path $candidates[0].FullName 'business.sqlite'
}
if(-not (Test-Path -LiteralPath $Database)){throw 'Existing personal database is missing. No new database was created.'}
if($CheckOnly){Write-Output 'Validated executable and existing personal database found. No server, account or data changed.';return}
$health=$null
try{$health=Invoke-RestMethod 'http://localhost:8080/api/health' -TimeoutSec 1}catch{}
if($health){
    if($health.runtime_id -ne 'personal-demo'){throw 'Port 8080 belongs to a different instance; stop that instance or use the independent Demo with another port.'}
    Write-Output 'Existing personal Demo is already running: http://localhost:8080'
    return
}
Write-Output 'Personal Demo: http://localhost:8080 . Original accounts and history are retained. Ctrl+C stops this foreground server.'
& $build.binary serve --db $Database --project $projectRoot --python (Join-Path $runtimeRoot 'tools/python/Scripts/python.exe') --runtime-id personal-demo
if($LASTEXITCODE -ne 0){throw 'Demo did not start. Existing data was retained; inspect the safe error above.'}
