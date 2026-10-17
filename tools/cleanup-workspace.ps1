param([switch]$Apply)
$ErrorActionPreference = 'Stop'
$chinaZone = [TimeZoneInfo]::FindSystemTimeZoneById('China Standard Time')
$cleanupNow = [TimeZoneInfo]::ConvertTimeFromUtc([DateTime]::UtcNow,$chinaZone)
if($cleanupNow.ToString('yyyy-MM-dd') -ne '2026-10-17'){throw 'This audited one-time cleanup allowlist is dated 2026-10-17; review a new allowlist before another cleanup.'}
# Explicit allowlist: only disposable copies and generated verification artifacts.
$workspaceRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..')).TrimEnd('\')
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..')).TrimEnd('\')
$runtimeRoot = Join-Path $env:LOCALAPPDATA 'dutucijp-vibecoding-final'
$verificationRoot = [IO.Path]::GetFullPath((Join-Path $runtimeRoot 'verification')).TrimEnd('\')
$recordRoot = Join-Path $projectRoot '每日工作记录/2026-10-17/原始输出/清理'
if(Test-Path -LiteralPath (Join-Path $recordRoot '清理结果.json')) {
    $prior = Get-Content -LiteralPath (Join-Path $recordRoot '清理结果.json') -Raw | ConvertFrom-Json
    if($prior.applied){throw 'This cleanup was already applied. Its receipt is retained; no repeat deletion was attempted.'}
}
New-Item -ItemType Directory -Path $recordRoot -Force | Out-Null
$targets = [Collections.Generic.List[object]]::new()
function Add-Target([string]$Path,[string]$Boundary,[string]$Reason) {
    if (-not (Test-Path -LiteralPath $Path)) { return }
    $resolved = (Resolve-Path -LiteralPath $Path).Path.TrimEnd('\')
    $boundaryPath = [IO.Path]::GetFullPath($Boundary).TrimEnd('\')
    if (-not $resolved.StartsWith($boundaryPath+'\',[StringComparison]::OrdinalIgnoreCase)) { throw "Unsafe cleanup target: $resolved" }
    $item = Get-Item -LiteralPath $resolved -Force
    if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "Refusing reparse point: $resolved" }
    $files = if ($item.PSIsContainer) { @(Get-ChildItem -LiteralPath $resolved -File -Force -Recurse) } else { @($item) }
    $targets.Add([pscustomobject]@{path=$resolved;bytes=[long](($files|Measure-Object Length -Sum).Sum);files=$files.Count;reason=$Reason})
}
foreach($name in @('教师独立Demo-候选包','教师独立Demo-交付候选','教师独立Demo-正式发行文件','教师验收 独立路径')) {
    Add-Target (Join-Path $workspaceRoot $name) $workspaceRoot '已由最终程序包和中期ZIP保留的候选副本'
}
# Keep the small launch/verification evidence, remove only the generated runtime.
foreach($name in @('教师独立Demo验收-空目录','教师独立Demo验收-二次空目录','教师独立Demo验收-最终版本')) {
    Add-Target (Join-Path $workspaceRoot "$name/local") $workspaceRoot '同机独立验收的临时数据库、准备结果及原始数据副本'
}
foreach($package in @('中期提交包-20261016-定稿','中期提交包-20261016-原样演示版','中期提交包-教师独立运行-20261016')) {
    foreach($name in @('解包核验','历史还原核验','教师版解包核验')) {
        Add-Target (Join-Path $workspaceRoot "$package/$name") $workspaceRoot '中期ZIP仍原样保留，移除已完成核验的解包/还原副本'
    }
}
foreach($root in @($projectRoot,(Join-Path $workspaceRoot '教师独立Demo工作区'))) {
    $backup = Join-Path $root '.backup'
    if(Test-Path -LiteralPath $backup) {
        Get-ChildItem -LiteralPath $backup -Directory -Force | Where-Object { $_.Name -match '^(remote-|restored-|midterm-reassembl)' } | ForEach-Object {
            Add-Target $_.FullName $backup '已完成散列核对的远端下载/历史还原副本；bundle及回执保留'
        }
    }
}
Get-ChildItem -LiteralPath $workspaceRoot -Directory -Force | Where-Object Name -Like '.chart-data-*' | ForEach-Object { Add-Target $_.FullName $workspaceRoot '临时绘图副本' }
if(Test-Path -LiteralPath $verificationRoot) {
    Get-ChildItem -LiteralPath $verificationRoot -File -Force -Recurse | Where-Object {
        $relative = $_.FullName.Substring($verificationRoot.Length+1)
        $relative -notmatch '^(transaction-20261017-|performance-input-template-20261015-165942\\|optimization-20261016-query2\\|optimization-20261016-formal\\NFR02-1\\)' -and
        ($_.Name -match '\.(sqlite|db)(-wal|-shm)?$' -or $_.Name -eq 'records.csv' -or $_.Extension -eq '.exe')
    } | ForEach-Object { Add-Target $_.FullName $verificationRoot '历史隔离验证可再生数据库/CSV/程序；原始计时JSON、日志及一个正式全量实例保留' }
}
$targets | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $recordRoot '清理清单.json') -Encoding utf8
if($Apply) {
    foreach($target in $targets) {
        # Recheck the exact resolved path immediately before recursive deletion.
        if((Resolve-Path -LiteralPath $target.path).Path.TrimEnd('\') -ne $target.path) { throw 'Target changed' }
        Remove-Item -LiteralPath $target.path -Recurse -Force
    }
}
$summary = [pscustomobject]@{applied=[bool]$Apply;targets=$targets.Count;bytes=[long](($targets|Measure-Object bytes -Sum).Sum);files=($targets|Measure-Object files -Sum).Sum;workspace=$workspaceRoot;runtime=$verificationRoot;retained='课程原件、主源码/Git、原未提交文件、原始数据、个人业务库/账户/历史、中期ZIP及演示文件、计时JSON日志、复跑模板和当前专项库'}
$summary | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $recordRoot '清理结果.json') -Encoding utf8
$summary | ConvertTo-Json
