param([switch]$CheckOnly)
$ErrorActionPreference = 'Stop'
$windowsModulePath = Join-Path $env:WINDIR 'System32/WindowsPowerShell/v1.0/Modules'
$env:PSModulePath=$windowsModulePath+';'+$env:PSModulePath
$projectRoot = Split-Path $PSScriptRoot -Parent
$demoLocal = Join-Path $env:USERPROFILE 'AppData/Local/Packages/OpenAI.Codex_2p2nqsd0c76g0/LocalCache/Local'
$demoRuntime = Join-Path $demoLocal 'dutucijp-vibecoding-final'
$demoBinary = Join-Path $demoRuntime 'builds/optimized-20261016/recon.exe'
$demoDatabase = Join-Path $demoRuntime 'demos/demo-20261015-170120-d36101c6/business.sqlite'
$expectedHash = '31eb1c6c75788bd26ff5dc6bdb8ecb7b48cdfa98d281a18fbf6b7d150f9c7c7a'
foreach ($path in @($demoBinary,$demoDatabase)) {
    if (-not (Test-Path -LiteralPath $path)) { throw "Prepared demo file is missing: $path. See the project run instructions." }
}
if ((Get-FileHash -LiteralPath $demoBinary -Algorithm SHA256).Hash.ToLowerInvariant() -ne $expectedHash) {
    throw 'The binary differs from the verified optimized demo; review the run instructions before using it.'
}
function Test-DemoHealth {
    try {
        $demoHomeResponse = Invoke-WebRequest -UseBasicParsing -Uri 'http://localhost:8080' -TimeoutSec 2
        if ($demoHomeResponse.StatusCode -ne 200 -or $demoHomeResponse.Content -notmatch '/assets/') { return $false }
        try { $null = Invoke-WebRequest -UseBasicParsing -Uri 'http://localhost:8080/api/runs' -TimeoutSec 2; return $false }
        catch { return ($null -ne $_.Exception.Response -and [int]$_.Exception.Response.StatusCode -eq 401) }
    } catch { return $false }
}
$matchingProcess = @(Get-CimInstance Win32_Process -Filter "Name='recon.exe'" | Where-Object { $_.ExecutablePath -eq $demoBinary -and $_.CommandLine -like ('*'+$demoDatabase+'*') })
$healthy = Test-DemoHealth
if ($CheckOnly) {
    [ordered]@{ actual_time=(Get-Date -Format o); binary_verified=$true; database_exists=$true; matching_processes=$matchingProcess.Count; homepage_and_unauthenticated_api_healthy=$healthy; started_process=$false } | ConvertTo-Json
    return
}
if ($healthy -and $matchingProcess.Count -gt 0) {
    Start-Process 'http://localhost:8080'
    Write-Output 'The verified demo is already running; opened http://localhost:8080.'
    return
}
if ($healthy -or $matchingProcess.Count -gt 0) { throw 'A server is present but does not match the expected healthy demo. No process was stopped.' }
$probe = [Net.Sockets.TcpClient]::new()
try { $probe.Connect('127.0.0.1',8080); $portBusy=$true } catch { $portBusy=$false } finally { $probe.Dispose() }
if ($portBusy) { throw 'Port 8080 is in use. Close the known application yourself before starting the demo.' }
$launchDirectory = Join-Path $demoRuntime ('verification/demo-launch-'+(Get-Date -Format 'yyyyMMdd-HHmmss')+'-'+[guid]::NewGuid().ToString('N').Substring(0,8))
New-Item -ItemType Directory -Path $launchDirectory | Out-Null
$savedLocal = $env:LOCALAPPDATA
try {
    $env:LOCALAPPDATA=$demoLocal
    $demoProcess=Start-Process -FilePath $demoBinary -ArgumentList @('serve','--db',('"'+$demoDatabase+'"'),'--project',('"'+$projectRoot+'"')) -WorkingDirectory $projectRoot -WindowStyle Hidden -RedirectStandardOutput (Join-Path $launchDirectory 'stdout.txt') -RedirectStandardError (Join-Path $launchDirectory 'stderr.txt') -PassThru
} finally { $env:LOCALAPPDATA=$savedLocal }
for ($attempt=0;$attempt -lt 30;$attempt++) {
    if ($demoProcess.HasExited) { throw "Demo exited. See $launchDirectory" }
    if (Test-DemoHealth) {
        [ordered]@{ actual_time=(Get-Date -Format o); pid=$demoProcess.Id; binary=$demoBinary; database=$demoDatabase; url='http://localhost:8080' } | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $launchDirectory 'launch.json') -Encoding UTF8
        Start-Process 'http://localhost:8080'
        Write-Output ('Demo is running. PID '+$demoProcess.Id+'. Logs: '+$launchDirectory)
        return
    }
    Start-Sleep -Milliseconds 200
}
throw "Demo did not become healthy in time. Inspect $launchDirectory; no existing process was stopped."
