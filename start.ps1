param([Parameter(Mandatory=$true)][string]$Database,[string]$Binary='')
$ErrorActionPreference='Stop'
$runtimeRoot=Join-Path $env:LOCALAPPDATA 'dutucijp-vibecoding-final'
if(-not $Binary){
 $latestBuild=Get-ChildItem -LiteralPath (Join-Path $runtimeRoot 'builds') -Filter recon.exe -File -Recurse -ErrorAction SilentlyContinue|Sort-Object LastWriteTime -Descending|Select-Object -First 1
 if(-not $latestBuild){throw 'Run ./build.ps1 on an allowed execution day before starting'}
 $Binary=$latestBuild.FullName
}
if(-not (Test-Path -LiteralPath $Database)){throw 'Database does not exist; prepare an independent runtime first'}
& $Binary serve --db $Database --project $PSScriptRoot
if($LASTEXITCODE -ne 0){throw 'Application stopped with an error'}
