param([Parameter(Mandatory=$true)][string]$SourceDatabase,[string]$Binary='')
$ErrorActionPreference='Stop'
. (Join-Path $PSScriptRoot 'tools/execution-day.ps1')
Assert-ExecutionDay
$runtimeRoot=Join-Path $env:LOCALAPPDATA 'dutucijp-vibecoding-final'
if(-not $Binary){$Binary=(Get-ChildItem -LiteralPath (Join-Path $runtimeRoot 'builds') -Filter recon.exe -Recurse -File|Sort-Object LastWriteTime -Descending|Select-Object -First 1).FullName}
if(-not $Binary){throw 'Run build.ps1 first'}
$demoRoot=Join-Path $runtimeRoot ('demos/demo-'+(Get-Date -Format 'yyyyMMdd-HHmmss')+'-'+[guid]::NewGuid().ToString('N').Substring(0,8))
New-Item -ItemType Directory -Path $demoRoot -Force|Out-Null
$database=Join-Path $demoRoot 'business.sqlite'
$python=Join-Path $runtimeRoot 'tools/python/Scripts/python.exe'
& $python (Join-Path $PSScriptRoot 'tools/prepare_demo.py') --source $SourceDatabase --destination $database
if($LASTEXITCODE -ne 0){throw 'Read-only demo copy failed'}
& $Binary init-auth --db $database --project $PSScriptRoot
if($LASTEXITCODE -ne 0){throw 'Local account initialization failed'}
Write-Output ('Start command: ./start.ps1 -Database "'+$database+'" -Binary "'+$Binary+'"')
Write-Output 'Open the private initialization links yourself. Real passwords, Passkeys, authenticator setup and recovery-code saving remain user actions.'
