function Assert-ExecutionDay {
    $taskZone = [TimeZoneInfo]::FindSystemTimeZoneById('China Standard Time')
    $taskNow = [TimeZoneInfo]::ConvertTimeFromUtc([DateTime]::UtcNow, $taskZone)
    if ($taskNow.ToString('MM-dd') -in @('10-22','11-17')) {
        throw 'Reserved day in Asia/Shanghai: no installation, preparation, rebuilding or verification work'
    }
}
