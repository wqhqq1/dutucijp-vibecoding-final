param([string]$Origin='http://localhost:18742')
$ErrorActionPreference='Stop'
$html=Invoke-WebRequest $Origin
if($html.StatusCode -ne 200){throw 'Homepage failed'}
$paths=[regex]::Matches($html.Content,'(?:src|href)="(/assets/[^"]+)"')|ForEach-Object {$_.Groups[1].Value}
$results=@()
foreach($path in $paths){
 $response=Invoke-WebRequest ($Origin+$path)
 $results += [ordered]@{path=$path;status=$response.StatusCode;content_type=$response.Headers['Content-Type'];bytes=$response.RawContentLength}
 if($path.EndsWith('.js') -and $response.Content -match '__mock|mock_view|mock-review-only'){throw 'Mock code in production bundle'}
 if($path.EndsWith('.css')){
  $fonts=[regex]::Matches($response.Content,'/assets/[^)"\s]+\.woff2?')|ForEach-Object {$_.Value}|Sort-Object -Unique
  foreach($font in $fonts){
   $fontResponse=Invoke-WebRequest ($Origin+$font)
   $expected=if($font.EndsWith('.woff2')){'font/woff2'}else{'font/woff'}
   if($fontResponse.Headers['Content-Type'] -notcontains $expected){throw "Font MIME mismatch: $font"}
   $results += [ordered]@{path=$font;status=$fontResponse.StatusCode;content_type=$fontResponse.Headers['Content-Type'];bytes=$fontResponse.RawContentLength}
  }
 }
}
$review=Invoke-WebRequest ($Origin+'/api/review-sets/latest?run_id=isolated') -SkipHttpErrorCheck
if($review.StatusCode -ne 401){throw 'Unauthenticated review endpoint did not reject access'}
$health=Invoke-RestMethod ($Origin+'/api/health')
[ordered]@{actual_time=(Get-Date -Format o);isolated=$true;health=$health;origin=$Origin;html_status=$html.StatusCode;csp=$html.Headers['Content-Security-Policy'];assets=$results;unauthenticated_review_status=$review.StatusCode;current_production_bundle_has_mock=$false}|ConvertTo-Json -Depth 6|Set-Content -LiteralPath (Join-Path $PSScriptRoot 'production-smoke-final.json') -Encoding utf8
