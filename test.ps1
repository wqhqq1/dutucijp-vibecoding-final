param([string]$OutputDir = '')
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'tools/execution-day.ps1')
Assert-ExecutionDay
. (Join-Path $PSScriptRoot 'tools/invoke-test-process.ps1')
$runtimeRoot = Join-Path $env:LOCALAPPDATA 'dutucijp-vibecoding-final'
$goExe = Join-Path $runtimeRoot 'tools/go/bin/go.exe'
$pythonExe = Join-Path $runtimeRoot 'tools/python/Scripts/python.exe'
$nodeExe = 'C:/Users/wqhqq/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin/node.exe'
if (-not (Test-Path -LiteralPath $nodeExe)) { $nodeExe = (Get-Command node -ErrorAction Stop).Source }
if (-not (Test-Path -LiteralPath $goExe) -or -not (Test-Path -LiteralPath $pythonExe)) { throw 'Run ./tools/bootstrap.ps1 first' }
if (-not $OutputDir) { $OutputDir = Join-Path $runtimeRoot ('verification/tests-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '-' + [guid]::NewGuid().ToString('N').Substring(0,8)) }
New-Item -ItemType Directory -Path $OutputDir -ErrorAction Stop | Out-Null
$OutputDir = (Resolve-Path -LiteralPath $OutputDir).Path
Push-Location $PSScriptRoot
try {
    $pythonOutput = Join-Path $OutputDir 'python.txt'
    $pythonStdout = Join-Path $OutputDir 'python.stdout.txt'
    $pythonExit = Invoke-TestProcess -FilePath $pythonExe -Arguments '-m unittest discover -s tests -p test_*.py -v' -WorkingDirectory $PSScriptRoot -StdoutPath $pythonStdout -StderrPath $pythonOutput
    $goOutput = Join-Path $OutputDir 'go.jsonl'
    $goStderr = Join-Path $OutputDir 'go.stderr.txt'
    $goExit = Invoke-TestProcess -FilePath $goExe -Arguments 'test -count=1 -json ./...' -WorkingDirectory (Join-Path $PSScriptRoot 'backend') -StdoutPath $goOutput -StderrPath $goStderr
    $frontendOutput = Join-Path $OutputDir 'frontend.tap.txt'
    $frontendStderr = Join-Path $OutputDir 'frontend.stderr.txt'
    $frontendExit = Invoke-TestProcess -FilePath $nodeExe -Arguments '--test --test-reporter=tap tests/requests.test.mjs tests/passkey-signals.test.mjs' -WorkingDirectory (Join-Path $PSScriptRoot 'frontend') -StdoutPath $frontendOutput -StderrPath $frontendStderr
    $frontendText = Get-Content -LiteralPath $frontendOutput -Raw -Encoding UTF8
    $frontendPassed = [regex]::Match($frontendText, '(?m)^# pass (\d+)').Groups[1].Value
    $events = @(Get-Content -LiteralPath $goOutput -Encoding UTF8 | ForEach-Object { try { $_ | ConvertFrom-Json -ErrorAction Stop } catch { } })
    $top = @($events | Where-Object { $_.Test -and $_.Test -notmatch '/' })
    $passed = @($top | Where-Object Action -EQ 'pass').Count
    $failed = @($top | Where-Object Action -EQ 'fail').Count
    $skipped = @($top | Where-Object Action -EQ 'skip').Count
    $pythonText = Get-Content -LiteralPath $pythonOutput -Raw -Encoding UTF8
    $pythonCount = [regex]::Match($pythonText, 'Ran (\d+) tests? in').Groups[1].Value
    $summary = [ordered]@{ actual_time=(Get-Date -Format o); powershell_version=$PSVersionTable.PSVersion.ToString(); go_exit=$goExit; python_exit=$pythonExit; frontend_exit=$frontendExit; go_top_level_passed=$passed; go_top_level_failed=$failed; go_top_level_skipped=$skipped; python_tests_ran=$pythonCount; frontend_passed=$frontendPassed; go_output=$goOutput; go_stderr=$goStderr; python_output=$pythonOutput; python_stdout=$pythonStdout; frontend_output=$frontendOutput; frontend_stderr=$frontendStderr }
    $summary | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $OutputDir 'summary.json') -Encoding utf8
    Get-Content -LiteralPath $pythonOutput -Encoding UTF8
    Get-Content -LiteralPath $pythonStdout -Encoding UTF8
    Get-Content -LiteralPath $goStderr -Encoding UTF8
    Get-Content -LiteralPath $frontendOutput -Encoding UTF8
    Get-Content -LiteralPath $frontendStderr -Encoding UTF8
    $summary | ConvertTo-Json
    if ($pythonExit -ne 0 -or $goExit -ne 0 -or $frontendExit -ne 0 -or $failed -gt 0 -or $passed -eq 0 -or -not $frontendPassed -or [int]$frontendPassed -eq 0) { throw 'Tests failed; see original output files' }
} finally { Pop-Location }
