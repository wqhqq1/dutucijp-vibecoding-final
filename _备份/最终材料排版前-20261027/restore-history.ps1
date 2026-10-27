param([string]$OutputPath='')
$ErrorActionPreference='Stop'
$m=Get-Content -LiteralPath (Join-Path $PSScriptRoot 'manifest.json') -Raw -Encoding utf8 | ConvertFrom-Json
if(-not $OutputPath){$OutputPath=Join-Path $PSScriptRoot '材料原生增量.bundle'}
if(Test-Path -LiteralPath $OutputPath){throw 'Output exists; choose a new path.'}
$o=[IO.File]::Open($OutputPath,[IO.FileMode]::CreateNew)
try{foreach($p in $m.parts){$f=Join-Path $PSScriptRoot $p.filename;if((Get-FileHash -LiteralPath $f -Algorithm SHA256).Hash.ToLowerInvariant() -ne $p.sha256){throw 'Part hash mismatch'};$i=[IO.File]::OpenRead($f);try{$i.CopyTo($o)}finally{$i.Dispose()}}}finally{$o.Dispose()}
if((Get-FileHash -LiteralPath $OutputPath -Algorithm SHA256).Hash.ToLowerInvariant() -ne $m.history_increment_sha256){throw 'Bundle hash mismatch'}
Write-Output ('Verified history increment: '+$OutputPath)
