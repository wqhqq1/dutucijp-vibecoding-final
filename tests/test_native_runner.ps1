param([string]$OutputDir = '')
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
. (Join-Path $projectRoot 'tools/execution-day.ps1')
Assert-ExecutionDay
. (Join-Path $projectRoot 'tools/invoke-test-process.ps1')
$runtimeRoot = Join-Path $env:LOCALAPPDATA 'dutucijp-vibecoding-final'
$python = Join-Path $runtimeRoot 'tools/python/Scripts/python.exe'
if (-not $OutputDir) { $OutputDir = Join-Path $runtimeRoot ('verification/native-runner-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '-' + [guid]::NewGuid().ToString('N').Substring(0,8)) }
New-Item -ItemType Directory -Path $OutputDir -ErrorAction Stop | Out-Null
$OutputDir = (Resolve-Path -LiteralPath $OutputDir).Path
$fixture = Join-Path $OutputDir 'probe.py'
$program = @'
import sys
sys.stdout.write("out-marker\u4e2d\u6587\n" * 8192)
sys.stdout.flush()
sys.stderr.write("err-marker\u4e2d\u6587\n" * 8192)
sys.stderr.flush()
raise SystemExit(int(sys.argv[1]))
'@
[IO.File]::WriteAllText($fixture, $program, (New-Object Text.UTF8Encoding($false)))
$results = @()
foreach ($expected in @(0,7)) {
    $stdout = Join-Path $OutputDir ('exit' + $expected + '.stdout.txt')
    $stderr = Join-Path $OutputDir ('exit' + $expected + '.stderr.txt')
    $actual = Invoke-TestProcess -FilePath $python -Arguments ('"' + $fixture + '" ' + $expected) -WorkingDirectory $projectRoot -StdoutPath $stdout -StderrPath $stderr
    $outText = Get-Content -LiteralPath $stdout -Raw -Encoding UTF8
    $errText = Get-Content -LiteralPath $stderr -Raw -Encoding UTF8
    if ($actual -ne $expected -or (Get-Item -LiteralPath $stdout).Length -le 65536 -or (Get-Item -LiteralPath $stderr).Length -le 65536 -or -not $outText.Contains('out-marker') -or $outText.Contains('err-marker') -or -not $errText.Contains('err-marker') -or $errText.Contains('out-marker')) {
        throw 'Native runner lost the exit code or stream separation/drainage'
    }
    if (-not $outText.Contains([char]0x4e2d) -or -not $errText.Contains([char]0x6587)) { throw 'UTF-8 stream output was corrupted' }
    $results += [pscustomobject]@{ expected_exit=$expected; actual_exit=$actual; stdout_bytes=(Get-Item -LiteralPath $stdout).Length; stderr_bytes=(Get-Item -LiteralPath $stderr).Length; unicode_preserved=$true; passed=$true }
}
$summary = [ordered]@{ actual_time=(Get-Date -Format o); powershell_version=$PSVersionTable.PSVersion.ToString(); fixture='Explicit subprocess probe only; exit7 is an expected runner counterexample, not a failing project test'; results=$results }
$summary | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $OutputDir 'summary.json') -Encoding UTF8
$summary | ConvertTo-Json -Depth 4
