param([string]$OutputDirectory = (Join-Path $PSScriptRoot '../../dist/windows-x64'))
$ErrorActionPreference = 'Stop'
$projectFile = Join-Path $PSScriptRoot 'Reconciliation.Windows.csproj'
$vswhere = Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio/Installer/vswhere.exe'
if (-not (Test-Path -LiteralPath $vswhere)) { throw '需要本地 Visual Studio Build Tools 的 WinUI/Windows SDK 资源构建任务。' }
$msbuildExe = & $vswhere -latest -products '*' -requires Microsoft.Component.MSBuild -find 'MSBuild\**\Bin\MSBuild.exe' | Select-Object -First 1
if (-not $msbuildExe) { throw '未找到 Visual Studio MSBuild。' }
$outputPath = [IO.Path]::GetFullPath($OutputDirectory)
New-Item -ItemType Directory -Force -Path $outputPath | Out-Null
& $msbuildExe $projectFile /restore /t:Publish /p:Configuration=Release /p:Platform=x64 /p:RuntimeIdentifier=win-x64 /p:SelfContained=true /p:WindowsAppSDKSelfContained=true "/p:PublishDir=$outputPath\" /v:minimal /nologo
if ($LASTEXITCODE -ne 0) { throw "WinUI 构建失败，退出码 $LASTEXITCODE" }
# WinUI's native DLLs also need the app-local, redistributable VC++ CRT.
$vsInstall = & $vswhere -latest -products '*' -requires Microsoft.Component.MSBuild -property installationPath | Select-Object -First 1
$crtDirectory = Get-ChildItem -LiteralPath (Join-Path $vsInstall 'VC/Redist/MSVC') -Directory | Where-Object Name -Match '^\d+\.\d+\.\d+$' | Sort-Object { [version]$_.Name } -Descending | ForEach-Object { Join-Path $_.FullName 'x64/Microsoft.VC143.CRT' } | Where-Object { Test-Path -LiteralPath $_ } | Select-Object -First 1
if (-not $crtDirectory) { throw '未找到可再分发的 x64 Visual C++ CRT。' }
Get-ChildItem -LiteralPath $crtDirectory -Filter '*.dll' -File | Copy-Item -Destination $outputPath -Force
$requiredFiles = @('Reconciliation.Windows.exe','Reconciliation.Windows.dll','Reconciliation.Windows.runtimeconfig.json','Reconciliation.Windows.pri','App.xbf','MainWindow.xbf','Microsoft.UI.Xaml.dll','coreclr.dll','hostfxr.dll','msvcp140.dll','vcruntime140.dll','vcruntime140_1.dll')
foreach ($name in $requiredFiles) { if (-not (Test-Path -LiteralPath (Join-Path $outputPath $name))) { throw "自包含依赖缺失：$name" } }
Copy-Item -LiteralPath (Join-Path $PSScriptRoot '运行说明.md') -Destination $outputPath -Force
Copy-Item -LiteralPath (Join-Path $PSScriptRoot '第三方依赖.md') -Destination $outputPath -Force
$noticePath=Join-Path $outputPath 'Notices'
New-Item -ItemType Directory -Force -Path $noticePath | Out-Null
Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'Notices/QRCoder.txt') -Destination $noticePath -Force
$nugetRoot=if ($env:NUGET_PACKAGES) {$env:NUGET_PACKAGES} else {Join-Path $env:USERPROFILE '.nuget/packages'}
$runtimeConfig=Get-Content -LiteralPath (Join-Path $outputPath 'Reconciliation.Windows.runtimeconfig.json') -Raw | ConvertFrom-Json
$netVersion=$runtimeConfig.runtimeOptions.includedFrameworks[0].version
Copy-Item -LiteralPath (Join-Path $nugetRoot "microsoft.netcore.app.runtime.win-x64/$netVersion/LICENSE.TXT") -Destination (Join-Path $noticePath 'dotnet-LICENSE.txt') -Force
Copy-Item -LiteralPath (Join-Path $nugetRoot "microsoft.netcore.app.runtime.win-x64/$netVersion/THIRD-PARTY-NOTICES.TXT") -Destination (Join-Path $noticePath 'dotnet-THIRD-PARTY-NOTICES.txt') -Force
Copy-Item -LiteralPath (Join-Path $nugetRoot 'microsoft.windowsappsdk/1.7.260224002/license.txt') -Destination (Join-Path $noticePath 'WindowsAppSDK-LICENSE.txt') -Force
$files = Get-ChildItem -LiteralPath $outputPath -Recurse -File | Where-Object Name -ne 'manifest.json' | ForEach-Object { [ordered]@{path=$_.FullName.Substring($outputPath.TrimEnd([IO.Path]::DirectorySeparatorChar).Length+1).Replace('\','/');length=$_.Length;sha256=(Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant()} }
[ordered]@{application='returns-recon-windows';version='1.0.0';platform='Windows 11 x64';api_version='1.0.0';windows_app_sdk='1.7.260224002';self_contained=$true;files=@($files)} | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $outputPath 'manifest.json') -Encoding utf8
Write-Output "免安装客户端：$(Join-Path $outputPath 'Reconciliation.Windows.exe')"
