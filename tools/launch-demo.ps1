param([switch]$CheckOnly,[int]$Port=8080,[string]$State='', [switch]$NoBrowser)
$ErrorActionPreference='Stop'
$projectRoot=Split-Path $PSScriptRoot -Parent
$teacherRoot=Join-Path $projectRoot 'teacher-demo'
$teacherPython=Join-Path $teacherRoot 'python/python.exe'
$teacherLauncher=Join-Path $teacherRoot 'portable/launcher.py'
if (-not (Test-Path -LiteralPath $teacherPython) -or -not (Test-Path -LiteralPath $teacherLauncher)) {
    throw 'The independent teacher-demo folder is missing. Use the full teacher delivery ZIP, not a source-only checkout. No author database is used.'
}
$teacherArguments=@($teacherLauncher)
if ($CheckOnly) { $teacherArguments+='check' } else { $teacherArguments+='start' }
$teacherArguments+=@('--port',[string]$Port)
if ($State) { $teacherArguments+=@('--state',$State) }
if ($NoBrowser) { $teacherArguments+='--no-browser' }
& $teacherPython @teacherArguments
if ($LASTEXITCODE -ne 0) { throw 'Independent Demo did not start; see its message and private runtime logs.' }
