param(
    [ValidateSet('All','NFR01','NFR02','NFR03')][string]$Measure = 'All',
    [string]$PreparedFull = '',
    [string]$InputTemplate = '',
    [string]$SourceCommit = '',
    [string]$OutputDir = ''
)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'execution-day.ps1')
Assert-ExecutionDay
$projectRoot = Split-Path -Parent $PSScriptRoot
$runtimeRoot = Join-Path $env:LOCALAPPDATA 'dutucijp-vibecoding-final'
$goExe = Join-Path $runtimeRoot 'tools/go/bin/go.exe'
$pythonExe = Join-Path $runtimeRoot 'tools/python/Scripts/python.exe'
$source = Join-Path $runtimeRoot 'raw/online_retail_II.xlsx'
foreach ($required in @($goExe,$pythonExe,$source)) {
    if (-not (Test-Path -LiteralPath $required)) { throw ('Missing prerequisite: ' + $required) }
}
if (-not $OutputDir) {
    $OutputDir = Join-Path $runtimeRoot ('verification/performance-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '-' + [guid]::NewGuid().ToString('N').Substring(0,8))
}
$resolvedOutput = [IO.Path]::GetFullPath($OutputDir)
$resolvedProject = [IO.Path]::GetFullPath($projectRoot).TrimEnd('\','/')
if ($resolvedOutput.Equals($resolvedProject,[StringComparison]::OrdinalIgnoreCase) -or $resolvedOutput.StartsWith($resolvedProject + [IO.Path]::DirectorySeparatorChar,[StringComparison]::OrdinalIgnoreCase)) {
    throw 'Performance databases and prepared raw rows must remain outside the repository'
}
if (Test-Path -LiteralPath $resolvedOutput) { throw 'OutputDir must be a new isolated directory' }
New-Item -ItemType Directory -Path $resolvedOutput | Out-Null
$OutputDir = (Resolve-Path -LiteralPath $resolvedOutput).Path

function Invoke-MeasuredProcess([string]$File, [string[]]$Arguments, [string]$Prefix, [string]$WorkingDirectory) {
    $info = [Diagnostics.ProcessStartInfo]::new()
    $info.FileName = $File
    $info.WorkingDirectory = $WorkingDirectory
    $info.UseShellExecute = $false
    $info.CreateNoWindow = $true
    $info.RedirectStandardOutput = $true
    $info.RedirectStandardError = $true
    foreach ($argument in $Arguments) { $info.ArgumentList.Add($argument) }
    $process = [Diagnostics.Process]::new()
    $process.StartInfo = $info
    $stopwatch = [Diagnostics.Stopwatch]::StartNew()
    $startedAt = [DateTimeOffset]::Now.ToString('o')
    if (-not $process.Start()) { throw ('Failed to start: ' + $File) }
    $stdoutTask = $process.StandardOutput.ReadToEndAsync()
    $stderrTask = $process.StandardError.ReadToEndAsync()
    $peak = 0L
    $rootPeak = 0L
    $memoryError = $null
    $tracked = [Collections.Generic.Dictionary[int,Diagnostics.Process]]::new()
    $tracked.Add($process.Id,$process)
    $perProcessPeak = [Collections.Generic.Dictionary[int,long]]::new()
    $pythonLauncher = [IO.Path]::GetFileName($File) -ieq 'python.exe'
    $childLookups = 0
    $nextChildLookup = 0.05
    do {
        # Windows virtual-environment python.exe can be a launcher. Discover its
        # actual interpreter child instead of reporting only the small launcher.
        if ($pythonLauncher -and $tracked.Count -eq 1 -and $childLookups -lt 3 -and $stopwatch.Elapsed.TotalSeconds -ge $nextChildLookup) {
            foreach ($child in @(Get-CimInstance Win32_Process -Filter ('ParentProcessId=' + $process.Id))) {
                try { $tracked.Add([int]$child.ProcessId,[Diagnostics.Process]::GetProcessById([int]$child.ProcessId)) } catch { $memoryError = $_.Exception.Message }
            }
            $childLookups++
            $nextChildLookup += 0.2
        }
        $treeWorkingSet = 0L
        foreach ($trackedProcess in @($tracked.Values)) {
            try {
                $trackedProcess.Refresh()
                $treeWorkingSet += $trackedProcess.WorkingSet64
                if (-not $perProcessPeak.ContainsKey($trackedProcess.Id)) { $perProcessPeak.Add($trackedProcess.Id,0L) }
                $perProcessPeak[$trackedProcess.Id] = [Math]::Max($perProcessPeak[$trackedProcess.Id],$trackedProcess.PeakWorkingSet64)
                if ($trackedProcess.Id -eq $process.Id) { $rootPeak = [Math]::Max($rootPeak,$trackedProcess.PeakWorkingSet64) }
            } catch { $memoryError = $_.Exception.Message }
        }
        $peak = [Math]::Max($peak,$treeWorkingSet)
        $exited = $process.WaitForExit(20)
    } while (-not $exited)
    $process.WaitForExit()
    $stopwatch.Stop()
    try { $process.Refresh(); $rootPeak = [Math]::Max($rootPeak,$process.PeakWorkingSet64) } catch { $memoryError = $_.Exception.Message }
    $peak = [Math]::Max($peak,$rootPeak)
    $stdout = $stdoutTask.GetAwaiter().GetResult()
    $stderr = $stderrTask.GetAwaiter().GetResult()
    [IO.File]::WriteAllText($Prefix + '.stdout.txt',$stdout,[Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText($Prefix + '.stderr.txt',$stderr,[Text.UTF8Encoding]::new($false))
    $result = [ordered]@{
        executable=$File; arguments=$Arguments; working_directory=$WorkingDirectory; started_at=$startedAt
        ended_at=[DateTimeOffset]::Now.ToString('o'); exit_code=$process.ExitCode; wall_seconds=$stopwatch.Elapsed.TotalSeconds
        peak_working_set_bytes=$(if ($peak -gt 0) {$peak} else {$null})
        root_peak_working_set_bytes=$rootPeak
        observed_process_peaks=@($perProcessPeak.GetEnumerator() | ForEach-Object { [ordered]@{pid=$_.Key; peak_working_set_bytes=$_.Value} })
        python_interpreter_child_observed=$(if ($pythonLauncher) {$tracked.Count -gt 1} else {$null})
        peak_memory_method='Maximum of sampled simultaneous root/interpreter-child working sets (20ms), and OS root process PeakWorkingSet64; per-process high-water marks are separate and never summed. Python launcher child discovered with Win32_Process.'
        peak_memory_error=$memoryError; stdout=($Prefix + '.stdout.txt'); stderr=($Prefix + '.stderr.txt')
    }
    $result | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath ($Prefix + '.process.json') -Encoding utf8
    foreach ($trackedProcess in @($tracked.Values)) { $trackedProcess.Dispose() }
    if ($result.exit_code -ne 0) { throw ('Measured command failed: ' + $Prefix + '; exit=' + $result.exit_code) }
    return [pscustomobject]$result
}

$binary = Join-Path $OutputDir 'perfverify.exe'
Push-Location (Join-Path $projectRoot 'backend')
try {
    & $goExe build -o $binary ./cmd/perfverify *> (Join-Path $OutputDir 'build.txt')
    if ($LASTEXITCODE -ne 0) { throw 'Performance driver build failed; no measurements published' }
} finally { Pop-Location }
$sourceStatus = @()
if (Test-Path -LiteralPath (Join-Path $projectRoot '.git')) {
    $actualSourceCommit = & git -C $projectRoot rev-parse HEAD
    $sourceStatus = @(& git -C $projectRoot status --short)
    if ($SourceCommit -and $actualSourceCommit -ne $SourceCommit) { throw 'Specified SourceCommit differs from the live checkout HEAD' }
    $SourceCommit = $actualSourceCommit
} elseif (-not $SourceCommit -or $SourceCommit -notmatch '^[0-9a-f]{40}$') {
    throw 'Exported source snapshot requires explicit full -SourceCommit provenance'
}
$environment = [ordered]@{
    recorded_at=[DateTimeOffset]::Now.ToString('o'); actual_timezone='Asia/Shanghai'; measure=$Measure
    git_head=$SourceCommit; git_status=$sourceStatus; source_snapshot_root=$projectRoot
    operating_system=[Environment]::OSVersion.VersionString; architecture=[Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
    powershell=$PSVersionTable.PSVersion.ToString(); logical_processors=[Environment]::ProcessorCount
    processors=@(Get-CimInstance Win32_Processor | Select-Object Name,NumberOfCores,NumberOfLogicalProcessors,MaxClockSpeed)
    physical_memory_bytes=(Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory
    go_version=(& $goExe version); python_version=(& $pythonExe --version); python_dependencies=@(& $pythonExe -m pip freeze)
    source_sha256=(Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash.ToLowerInvariant()
    source_read_only=((Get-Item -LiteralPath $source).Attributes -band [IO.FileAttributes]::ReadOnly) -ne 0
    binary_sha256=(Get-FileHash -LiteralPath $binary -Algorithm SHA256).Hash.ToLowerInvariant()
    binary_build_info=@(& $goExe version -m $binary)
    code_hashes=@(Get-ChildItem -LiteralPath (Join-Path $projectRoot 'backend') -Recurse -File | Where-Object Extension -In '.go','.sql' | ForEach-Object { [ordered]@{path=$_.FullName.Substring($projectRoot.Length+1); sha256=(Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant()} })
    prepare_sha256=(Get-FileHash -LiteralPath (Join-Path $projectRoot 'tools/prepare.py') -Algorithm SHA256).Hash.ToLowerInvariant()
    boundary='No downloads, installation or compilation timed. Fresh preparation output for each NFR01/NFR02; new empty SQLite for every trial. OS filesystem cache is not deliberately flushed. Post-timing durable checks are separate.'
}
$environment | ConvertTo-Json -Depth 10 | Set-Content -LiteralPath (Join-Path $OutputDir 'environment.json') -Encoding utf8
if (-not $environment.source_read_only) { throw 'Official source must already be read-only' }
$trials = [Collections.Generic.List[object]]::new()
foreach ($nfr in @('NFR01','NFR02','NFR03')) {
    if ($Measure -ne 'All' -and $Measure -ne $nfr) { continue }
    if ($nfr -eq 'NFR03' -and -not $PreparedFull) { throw 'NFR03 requires an already prepared official full-data directory via -PreparedFull, or -Measure All' }
    for ($iteration=1; $iteration -le 3; $iteration++) {
        Assert-ExecutionDay
        $trialDir = Join-Path $OutputDir ($nfr + '-' + $iteration)
        New-Item -ItemType Directory -Path $trialDir | Out-Null
        $scope = $(if ($nfr -eq 'NFR01') {'benchmark'} else {'all'})
        $preparation = $null
        $whole = [Diagnostics.Stopwatch]::StartNew()
        if ($nfr -ne 'NFR03') {
            $preparedRoot = Join-Path $trialDir 'prepared'
            $preparation = Invoke-MeasuredProcess $pythonExe @((Join-Path $projectRoot 'tools/prepare.py'),'--source',$source,'--scope',$scope,'--output',$preparedRoot) (Join-Path $trialDir 'prepare') $projectRoot
            $lastLine = (Get-Content -LiteralPath $preparation.stdout | Select-Object -Last 1) | ConvertFrom-Json
            if ($lastLine.stage -ne 'prepared' -or $lastLine.scope -ne $scope) { throw 'Fresh complete preparation not demonstrated' }
            $prepared = $lastLine.path
            if ($scope -eq 'all') { $PreparedFull = $prepared }
        } else { $prepared = $PreparedFull }
        $mode = $(if ($nfr -eq 'NFR03') {'recompute'} else {'full'})
        $goArguments = @('--prepared',$prepared,'--db',(Join-Path $trialDir 'isolated.sqlite'),'--mode',$mode)
        if ($nfr -eq 'NFR03' -and $InputTemplate) { $goArguments += @('--input-template',$InputTemplate) }
        $goProcess = Invoke-MeasuredProcess $binary $goArguments (Join-Path $trialDir 'go') $projectRoot
        $whole.Stop()
        $goReport = Get-Content -LiteralPath $goProcess.stdout -Raw | ConvertFrom-Json
        $seconds = $(if ($nfr -eq 'NFR03') {$goReport.measured_seconds} else {$whole.Elapsed.TotalSeconds})
        $limit = $(if ($nfr -eq 'NFR01') {5} elseif ($nfr -eq 'NFR02') {90} else {15})
        $target = $(if ($nfr -eq 'NFR02') {45} else {5})
        if ($goReport.manifest.scope -ne $scope -or $goReport.runs_before -ne 0 -or $goReport.summary.reused) { throw 'Isolation/scope/recalculation invariant failed' }
        if ($nfr -eq 'NFR01' -and $goReport.manifest.stats.raw -ne 10000) { throw 'Benchmark is not exactly 10000 original rows' }
        if ($scope -eq 'all' -and $goReport.manifest.stats.raw -ne 1067371) { throw 'Official all scope is incomplete' }
        $peakValues = @($goProcess.peak_working_set_bytes)
        if ($preparation) { $peakValues += $preparation.peak_working_set_bytes }
        $trial = [ordered]@{
            nfr=$nfr; iteration=$iteration; scope=$scope; measured_seconds=$seconds; limit_seconds=$limit; target_seconds=$target
            baseline_passed=($seconds -le $limit); target_passed=($seconds -le $target)
            peak_working_set_bytes=($peakValues | Measure-Object -Maximum).Maximum
            preparation=$preparation; go_process=$goProcess; go_report=$goReport
            timing_boundary=$(if ($nfr -eq 'NFR03') {'Store.Run including input load, matching, case/statistics persistence, publication; plus close/flush. Initial import reported separately, excluded from recompute metric. Process peak memory includes initial input import and is not a compute-only peak.'} else {'External wall from fresh official XLSX preparation through full Go process completion, including import, computation, difference/statistics persistence and close. Go post-timing durable verification also included conservatively.'})
        }
        $trials.Add([pscustomobject]$trial)
        $trial | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath (Join-Path $trialDir 'trial.json') -Encoding utf8
        $trials | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath (Join-Path $OutputDir 'trials.json') -Encoding utf8
        Write-Output ('{0} trial {1}: {2:N6}s; baseline {3}; peak {4:N0} bytes' -f $nfr,$iteration,$seconds,$trial.baseline_passed,$trial.peak_working_set_bytes)
    }
}
$summary = [ordered]@{
    actual_time=[DateTimeOffset]::Now.ToString('o'); output_dir=$OutputDir; environment=(Join-Path $OutputDir 'environment.json')
    prepared_full=$PreparedFull; results=@($trials | Select-Object nfr,iteration,scope,measured_seconds,limit_seconds,target_seconds,baseline_passed,target_passed,peak_working_set_bytes)
    all_measured_baselines_passed=(@($trials | Where-Object { -not $_.baseline_passed }).Count -eq 0)
    nfr04='Not executed by this script; requires authenticated HTTP service, live long task, 10 workers and at least 300 requests.'
}
$summary | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $OutputDir 'summary.json') -Encoding utf8
$summary | ConvertTo-Json -Depth 8
if (-not $summary.all_measured_baselines_passed) { throw 'One or more actual measurements failed their specified baseline; preserve raw outputs' }
