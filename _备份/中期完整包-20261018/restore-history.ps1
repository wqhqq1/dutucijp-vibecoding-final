param([string]$OutputPath='')
$ErrorActionPreference='Stop'
$manifest=Get-Content -LiteralPath (Join-Path $PSScriptRoot 'manifest.json') -Raw -Encoding utf8 | ConvertFrom-Json
if(-not $OutputPath){$OutputPath=Join-Path $PSScriptRoot ([IO.Path]::GetFileName($manifest.history.increment_path))}
if(Test-Path -LiteralPath $OutputPath){throw 'Output already exists; use another path.'}
$stream=[IO.File]::Open($OutputPath,[IO.FileMode]::CreateNew)
try{foreach($part in $manifest.history.increment_parts){$path=Join-Path $PSScriptRoot $part.filename;if((Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant() -ne $part.sha256){throw ('Part hash mismatch: '+$part.filename)};$inputStream=[IO.File]::OpenRead($path);try{$inputStream.CopyTo($stream)}finally{$inputStream.Dispose()}}}finally{$stream.Dispose()}
if((Get-FileHash -LiteralPath $OutputPath -Algorithm SHA256).Hash.ToLowerInvariant() -ne $manifest.history.increment_sha256){throw 'History bundle hash mismatch'}
Write-Output ('Verified native history increment: '+$OutputPath)
