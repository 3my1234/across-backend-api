$ErrorActionPreference = 'Stop'
$collector = Join-Path $PSScriptRoot 'collect_cohost_prerequisites_readonly.py'
$stamp = [DateTime]::UtcNow.ToString('yyyyMMddTHHmmssZ')
$destination = Join-Path $PSScriptRoot ('cohost-prerequisites-' + $stamp)
New-Item -ItemType Directory -Path $destination | Out-Null
foreach ($target in @(@{Role='main';HostName='173.212.219.169'}, @{Role='database';HostName='79.143.176.173'})) {
    Write-Host ('Read-only ' + $target.Role + ' prerequisite check. Enter SSH password locally.')
    $result = Get-Content -Raw -LiteralPath $collector | & ssh ('root@' + $target.HostName) python3 - --role $target.Role
    if ($LASTEXITCODE -ne 0) { throw ('SSH check failed: ' + $target.Role) }
    $json = $result -join "`n"
    $null = $json | ConvertFrom-Json
    [System.IO.File]::WriteAllText((Join-Path $destination ($target.Role + '.json')), $json)
}
[System.IO.File]::WriteAllText((Join-Path $PSScriptRoot 'cohost-prerequisites-latest.txt'), $destination)
Write-Host 'Done. Reports saved; no need to paste them.'
