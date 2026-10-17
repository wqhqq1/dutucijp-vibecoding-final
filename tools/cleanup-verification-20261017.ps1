# One-time cleanup prepared at the user's request. Default: preview only.
# Run with -Apply only after the user authorizes deletion.
# Keep the adjacent .manifest.json file with this script.
[CmdletBinding(SupportsShouldProcess = $true)]
param([switch]$Apply)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2.0
$manifestPath = Join-Path $PSScriptRoot 'cleanup-verification-20261017.manifest.json'
$manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
$verificationRoot = [IO.Path]::GetFullPath((Join-Path $env:LOCALAPPDATA 'dutucijp-vibecoding-final\verification')).TrimEnd('\')
$allowed = @(
    'transaction-20261017-baseline',
    'transaction-20261017-optimized',
    'transaction-20261017-optimized-final',
    'transaction-20261017-browser',
    'transaction-20261017-nfr04',
    'portable-current-20261017\local'
)
if ($manifest.version -ne 1 -or $manifest.verification_root -ne $verificationRoot) {
    throw 'Manifest belongs to a different runtime location. No deletion attempted.'
}
if (@($manifest.targets).Count -ne $allowed.Count) { throw 'Unexpected manifest target count.' }

function Assert-PlainPath([string]$Path) {
    $cursor = $Path
    while ($cursor) {
        $item = Get-Item -LiteralPath $cursor -Force
        if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) {
            throw "Refusing junction/symbolic link: $cursor"
        }
        $cursor = [IO.Path]::GetDirectoryName($cursor)
    }
}

function Get-SafeInventory([string]$Path) {
    $full = [IO.Path]::GetFullPath($Path).TrimEnd('\')
    if (-not $full.StartsWith($verificationRoot + '\', [StringComparison]::OrdinalIgnoreCase)) {
        throw "Target escapes verification root: $full"
    }
    Assert-PlainPath $full
    $rootItem = Get-Item -LiteralPath $full -Force
    if (-not $rootItem.PSIsContainer) { throw "Expected directory: $full" }
    if ((Resolve-Path -LiteralPath $full).ProviderPath.TrimEnd('\') -ne $full) {
        throw "Resolved target changed: $full"
    }
    $pending = [Collections.Generic.Stack[string]]::new()
    $pending.Push($full)
    $entries = [Collections.Generic.List[object]]::new()
    while ($pending.Count -gt 0) {
        $directory = $pending.Pop()
        foreach ($item in Get-ChildItem -LiteralPath $directory -Force) {
            if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) {
                throw "Refusing junction/symbolic link: $($item.FullName)"
            }
            $relative = $item.FullName.Substring($full.Length + 1)
            if ($item.PSIsContainer) {
                $entries.Add([pscustomobject]@{kind='directory'; relative=$relative; bytes=0; modified_ticks=''})
                $pending.Push($item.FullName)
            } else {
                $entries.Add([pscustomobject]@{
                    kind='file'; relative=$relative; bytes=[long]$item.Length
                    modified_ticks=$item.LastWriteTimeUtc.Ticks.ToString([Globalization.CultureInfo]::InvariantCulture)
                })
            }
        }
    }
    $entries | Sort-Object relative, kind
}

function Assert-Inventory([object]$Target, [string]$Path) {
    $current = @(Get-SafeInventory $Path)
    $expectedKeys = @($Target.entries | ForEach-Object { "$($_.kind)|$($_.relative)|$($_.bytes)|$($_.modified_ticks)" })
    $currentKeys = @($current | ForEach-Object { "$($_.kind)|$($_.relative)|$($_.bytes)|$($_.modified_ticks)" })
    if ($expectedKeys.Count -ne $currentKeys.Count -or
        @(Compare-Object -ReferenceObject $expectedKeys -DifferenceObject $currentKeys).Count -gt 0) {
        throw "Directory changed since the reviewed snapshot; review a new manifest before deleting: $Path"
    }
}

# Validate every target before any deletion; missing directories are harmless.
$plan = @()
for ($i = 0; $i -lt $allowed.Count; $i++) {
    $target = $manifest.targets[$i]
    if ($target.relative -ne $allowed[$i]) { throw 'Manifest differs from the fixed six-directory allowlist.' }
    $path = [IO.Path]::GetFullPath((Join-Path $verificationRoot $allowed[$i])).TrimEnd('\')
    $exists = Test-Path -LiteralPath $path
    if ($exists) { Assert-Inventory $target $path }
    $bytes = [long](($target.entries | Where-Object kind -eq 'file' | Measure-Object bytes -Sum).Sum)
    $plan += [pscustomobject]@{path=$path; exists=[bool]$exists; bytes=$bytes; status='pending'}
}
$present = @($plan | Where-Object exists)
$totalBytes = [long](($present | Measure-Object bytes -Sum).Sum)
$plan | Select-Object path, exists, bytes | Format-List | Out-Host
Write-Host ('Present: {0} directories; {1:N2} GiB ({2} bytes).' -f $present.Count, ($totalBytes / 1GB), $totalBytes)
if (-not $Apply -or $WhatIfPreference) {
    Write-Host 'PREVIEW ONLY. Nothing deleted. After approval, run this script with -Apply.'
    return
}
if ($present.Count -eq 0) { Write-Host 'Nothing remains to delete.'; return }

# Refuse to delete data referenced by a running process, including isolated servers.
$processes = @(Get-CimInstance Win32_Process -ErrorAction Stop)
foreach ($process in $processes) {
    foreach ($target in $present) {
        foreach ($value in @([string]$process.ExecutablePath, [string]$process.CommandLine)) {
            if ($value.Replace('/', '\').IndexOf($target.path, [StringComparison]::OrdinalIgnoreCase) -ge 0) {
                throw "Target is referenced by running process $($process.ProcessId). Stop that isolated process first: $($target.path)"
            }
        }
    }
}

$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$receiptRoot = Join-Path $projectRoot '每日工作记录\2026-10-17\原始输出\清理'
New-Item -ItemType Directory -Path $receiptRoot -Force | Out-Null
$receiptPath = Join-Path $receiptRoot ('临时库清理执行-{0}-{1}.json' -f (Get-Date -Format 'yyyyMMdd-HHmmss-fff'), [Guid]::NewGuid().ToString('N').Substring(0,8))
$receipt = [pscustomobject]@{
    started_at=[DateTimeOffset]::Now.ToString('o'); finished_at=$null
    manifest_sha256=(Get-FileHash -LiteralPath $manifestPath -Algorithm SHA256).Hash
    planned_bytes=$totalBytes; deleted_bytes=[long]0; completed=$false; error=$null; targets=$plan
}
function Save-Receipt {
    $receipt | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $receiptPath -Encoding UTF8
}
Save-Receipt
try {
    for ($i = 0; $i -lt $plan.Count; $i++) {
        $target = $plan[$i]
        if (-not $target.exists) { $target.status = 'already_absent'; continue }
        if ($PSCmdlet.ShouldProcess($target.path, 'Permanently delete this disposable verification directory')) {
            # Recheck the absolute boundary, links and complete snapshot just before removal.
            Assert-Inventory $manifest.targets[$i] $target.path
            Remove-Item -LiteralPath $target.path -Recurse -Force -ErrorAction Stop
            if (Test-Path -LiteralPath $target.path) { throw "Deletion incomplete: $($target.path)" }
            $target.status = 'deleted'
            $receipt.deleted_bytes += $target.bytes
        } else {
            $target.status = 'skipped'
        }
        Save-Receipt
    }
    $receipt.completed = @($plan | Where-Object { $_.status -notin @('deleted','already_absent') }).Count -eq 0
} catch {
    $receipt.error = $_.Exception.Message
    throw
} finally {
    $receipt.finished_at = [DateTimeOffset]::Now.ToString('o')
    Save-Receipt
    Write-Host "Receipt: $receiptPath"
}
