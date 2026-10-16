param([string]$OutputDirectory = (Join-Path $PSScriptRoot '还原中期包'))
$ErrorActionPreference = 'Stop'
$manifest = Get-Content -LiteralPath (Join-Path $PSScriptRoot '分卷清单.json') -Raw -Encoding UTF8 | ConvertFrom-Json
foreach ($part in $manifest.parts) {
    $partPath = Join-Path $PSScriptRoot $part.name
    if (-not (Test-Path -LiteralPath $partPath)) { throw "Missing part: $($part.name)" }
    if ((Get-FileHash -LiteralPath $partPath -Algorithm SHA256).Hash.ToLowerInvariant() -ne $part.sha256) { throw "Hash mismatch: $($part.name)" }
}
[void][System.IO.Directory]::CreateDirectory($OutputDirectory)
$target = Join-Path $OutputDirectory $manifest.archive_name
if (Test-Path -LiteralPath $target) {
    if ((Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash.ToLowerInvariant() -ne $manifest.zip_sha256) { throw 'Output exists with a different hash; choose another output directory' }
    Write-Output $target
    return
}
$output = [System.IO.File]::Open($target,[System.IO.FileMode]::CreateNew)
try {
    foreach ($part in $manifest.parts) {
        $inputStream = [System.IO.File]::OpenRead((Join-Path $PSScriptRoot $part.name))
        try { $inputStream.CopyTo($output) } finally { $inputStream.Dispose() }
    }
} finally { $output.Dispose() }
if ((Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash.ToLowerInvariant() -ne $manifest.zip_sha256) { throw 'Reassembled ZIP hash mismatch' }
Write-Output $target
