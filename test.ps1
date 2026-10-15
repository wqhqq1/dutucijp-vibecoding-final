param([string]$OutputDir = '')
$ErrorActionPreference = 'Stop'
$runtimeRoot = Join-Path $env:LOCALAPPDATA 'dutucijp-vibecoding-final'
$goExe = Join-Path $runtimeRoot 'tools/go/bin/go.exe'
$pythonExe = Join-Path $runtimeRoot 'tools/python/Scripts/python.exe'
if (-not (Test-Path -LiteralPath $goExe) -or -not (Test-Path -LiteralPath $pythonExe)) { throw 'Run ./tools/bootstrap.ps1 first' }
if (-not $OutputDir) { $OutputDir = Join-Path $runtimeRoot ('verification/tests-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '-' + [guid]::NewGuid().ToString('N').Substring(0,8)) }
New-Item -ItemType Directory -Path $OutputDir -ErrorAction Stop | Out-Null
$OutputDir = (Resolve-Path -LiteralPath $OutputDir).Path
Push-Location $PSScriptRoot
try {
    $pythonOutput = Join-Path $OutputDir 'python.txt'
    & $pythonExe -m unittest discover -s tests -p 'test_*.py' -v *> $pythonOutput
    $pythonExit = $LASTEXITCODE
    $goOutput = Join-Path $OutputDir 'go.jsonl'
    Push-Location (Join-Path $PSScriptRoot 'backend')
    try { & $goExe test -count=1 -json ./... *> $goOutput; $goExit = $LASTEXITCODE } finally { Pop-Location }
    $events = @(Get-Content -LiteralPath $goOutput | ForEach-Object { try { $_ | ConvertFrom-Json -ErrorAction Stop } catch { } })
    $top = @($events | Where-Object { $_.Test -and $_.Test -notmatch '/' })
    $passed = @($top | Where-Object Action -EQ 'pass').Count
    $failed = @($top | Where-Object Action -EQ 'fail').Count
    $skipped = @($top | Where-Object Action -EQ 'skip').Count
    $pythonText = Get-Content -LiteralPath $pythonOutput -Raw
    $pythonCount = [regex]::Match($pythonText, 'Ran (\d+) tests? in').Groups[1].Value
    $summary = [ordered]@{ actual_time=(Get-Date -Format o); go_exit=$goExit; python_exit=$pythonExit; go_top_level_passed=$passed; go_top_level_failed=$failed; go_top_level_skipped=$skipped; python_tests_ran=$pythonCount; go_output=$goOutput; python_output=$pythonOutput }
    $summary | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $OutputDir 'summary.json') -Encoding utf8
    Get-Content -LiteralPath $pythonOutput
    $summary | ConvertTo-Json
    if ($pythonExit -ne 0 -or $goExit -ne 0 -or $failed -gt 0 -or $passed -eq 0) { throw 'Tests failed; see original output files' }
} finally { Pop-Location }
