param([string]$OutputDirectory='')
$ErrorActionPreference='Stop'
. (Join-Path $PSScriptRoot 'tools/execution-day.ps1')
Assert-ExecutionDay
$runtimeRoot=Join-Path $env:LOCALAPPDATA 'dutucijp-vibecoding-final'
$goExe=Join-Path $runtimeRoot 'tools/go/bin/go.exe'
$nodeExe=(Get-Command node -ErrorAction Stop).Source
$nodeDir=Split-Path $nodeExe -Parent
$npmDirectory=Split-Path (Get-Command npm -ErrorAction Stop).Source -Parent
$npmCli=Join-Path $npmDirectory 'node_modules/npm/bin/npm-cli.js'
if(-not (Test-Path -LiteralPath $npmCli)){throw 'Official npm CLI is unavailable; see tools/environment.lock.json'}
if(-not $OutputDirectory){$OutputDirectory=Join-Path $runtimeRoot ('builds/'+(Get-Date -Format 'yyyyMMdd-HHmmss')+'-'+[guid]::NewGuid().ToString('N').Substring(0,8))}
New-Item -ItemType Directory -Path $OutputDirectory -Force | Out-Null
$OutputDirectory=(Resolve-Path -LiteralPath $OutputDirectory).Path
$savedPath=$env:PATH
try{
 $env:PATH=$nodeDir+';'+$env:PATH
 Push-Location (Join-Path $PSScriptRoot 'frontend')
 try{
  & $nodeExe $npmCli ci --registry=https://registry.npmjs.org --ignore-scripts
  if($LASTEXITCODE -ne 0){throw 'Locked frontend install failed'}
  & $nodeExe $npmCli run build
  if($LASTEXITCODE -ne 0){throw 'Frontend build failed'}
 }finally{Pop-Location}
 $assetDir=Join-Path $PSScriptRoot 'backend/internal/web/assets'
 New-Item -ItemType Directory -Path $assetDir -Force | Out-Null
 Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'frontend/dist/index.html') -Destination (Join-Path $assetDir 'index.html') -Force
 Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'frontend/dist/assets') -Destination $assetDir -Recurse -Force
 Push-Location (Join-Path $PSScriptRoot 'backend')
 try{& $goExe build -o (Join-Path $OutputDirectory 'recon.exe') ./cmd/recon;if($LASTEXITCODE -ne 0){throw 'Backend build failed'}}finally{Pop-Location}
 [ordered]@{actual_time=(Get-Date -Format o);binary=(Join-Path $OutputDirectory 'recon.exe');sha256=(Get-FileHash -LiteralPath (Join-Path $OutputDirectory 'recon.exe') -Algorithm SHA256).Hash.ToLowerInvariant()}|ConvertTo-Json|Set-Content -LiteralPath (Join-Path $OutputDirectory 'build.json') -Encoding utf8
 Get-Content -LiteralPath (Join-Path $OutputDirectory 'build.json')
}finally{$env:PATH=$savedPath}
