$ErrorActionPreference = 'Stop'
$workspace = 'C:\Users\EMMA\Desktop\gouwu\qualification'
$source = Join-Path $workspace 'collect_cohost_transport_failure.py'
foreach ($role in @('main','database')) {
    $address = if ($role -eq 'main') {'173.212.219.169'} else {'79.143.176.173'}
    Write-Host "Read-only $role test-tunnel diagnostics. Enter the SSH password locally. No setup or load."
    $result = Get-Content -Raw -LiteralPath $source | ssh -o ServerAliveInterval=15 -o ServerAliveCountMax=6 -o StrictHostKeyChecking=yes "root@$address" "python3 - --role $role"
    if ($LASTEXITCODE -ne 0) {throw "$role diagnostic failed; no changes made."}
    $text=$result -join "`n"
    $null=$text | ConvertFrom-Json
    [IO.File]::WriteAllText((Join-Path $workspace "cohost-transport-failure-$role.json"),$text,(New-Object System.Text.UTF8Encoding($false)))
}
Write-Host 'Both reports saved. Tell Codex done; no need to paste them. No resources were started.'
