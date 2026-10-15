param([string]$OutputPath = '')
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'execution-day.ps1')
Assert-ExecutionDay
$toolLock = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'environment.lock.json') -Raw | ConvertFrom-Json
$runtimeRoot = Join-Path $env:LOCALAPPDATA 'dutucijp-vibecoding-final'
$goExe = Join-Path $runtimeRoot 'tools/go/bin/go.exe'
$pythonExe = Join-Path $runtimeRoot 'tools/python/Scripts/python.exe'
$toolSpecs = @(
    @{name='Go'; path=$goExe; args=@('version'); expected=('go version '+$toolLock.go.version+' windows/amd64')},
    @{name='Node'; path=(Get-Command node -CommandType Application | Select-Object -First 1).Source; args=@('--version'); expected=$toolLock.node},
    @{name='npm'; path=(Get-Command npm.cmd -CommandType Application | Select-Object -First 1).Source; args=@('--version'); expected=$toolLock.npm},
    @{name='Python'; path=$pythonExe; args=@('--version'); expected=('Python '+$toolLock.python)},
    @{name='Git'; path=(Get-Command git -CommandType Application | Select-Object -First 1).Source; args=@('--version'); expected=$toolLock.git},
    @{name='Codex CLI'; path=(Get-Command codex -CommandType Application | Select-Object -First 1).Source; args=@('--version'); expected=$toolLock.codex_cli}
)
$observations = @()
foreach ($toolSpec in $toolSpecs) {
    $versionArgs = $toolSpec.args
    $actual = (& $toolSpec.path @versionArgs | Out-String).Trim()
    $toolExit = $LASTEXITCODE
    $signature = Get-AuthenticodeSignature -LiteralPath $toolSpec.path
    $observations += [ordered]@{name=$toolSpec.name; path=$toolSpec.path; expected=$toolSpec.expected; actual=$actual; exit=$toolExit; matches=($toolExit -eq 0 -and $actual -eq $toolSpec.expected); sha256=(Get-FileHash -LiteralPath $toolSpec.path).Hash.ToLowerInvariant(); signature=$signature.Status.ToString(); signer=$signature.SignerCertificate.Subject}
}
$packageJSON = & $pythonExe -c 'import importlib.metadata,json,sqlite3; print(json.dumps({"packages": {x:importlib.metadata.version(x) for x in ["pip","openpyxl","et_xmlfile"]},"official_wheel_receipts":{x:json.loads(importlib.metadata.distribution(x).read_text("direct_url.json") or "null") for x in ["openpyxl","et_xmlfile"]},"python_sqlite":sqlite3.sqlite_version}))'
if ($LASTEXITCODE -ne 0) { throw 'Python package version probe failed' }
$packages = $packageJSON | ConvertFrom-Json
Push-Location (Join-Path $PSScriptRoot '../backend')
try {
    $modules = & $goExe list -m all
    if ($LASTEXITCODE -ne 0) { throw 'Go module version probe failed' }
    $moduleVerification = & $goExe mod verify
    if ($LASTEXITCODE -ne 0) { throw 'Go module verification failed' }
} finally { Pop-Location }
$archivePath = Join-Path $runtimeRoot ('tools/' + $toolLock.go.filename)
$archiveHash = (Get-FileHash -LiteralPath $archivePath).Hash.ToLowerInvariant()
$result = [ordered]@{actual_time=([DateTimeOffset]::UtcNow.ToOffset([TimeSpan]::FromHours(8)).ToString('o')); tools=$observations; python=$packages; go_modules=$modules; go_module_verification=$moduleVerification; go_official_archive_sha256=$archiveHash; go_archive_matches=($archiveHash -eq $toolLock.go.sha256); note='Existing tools verified in place; CLI version is not a desktop-app version or a human-run claim.'}
$json = $result | ConvertTo-Json -Depth 8
if ($OutputPath) { $json | Set-Content -LiteralPath $OutputPath -Encoding utf8 }
Write-Output $json
if (@($observations | Where-Object { -not $_.matches }).Count -gt 0 -or $archiveHash -ne $toolLock.go.sha256 -or $packages.packages.openpyxl -ne '3.1.5' -or $packages.packages.et_xmlfile -ne '2.0.0') { throw 'Environment differs from locked versions; see observations' }
