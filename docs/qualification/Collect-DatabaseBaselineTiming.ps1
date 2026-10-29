$ErrorActionPreference = 'Stop'
$workspace = 'C:\Users\EMMA\Desktop\gouwu\qualification'
$source = Join-Path $workspace 'diagnose_database_baseline_timing.py'
$output = Join-Path $workspace 'database-baseline-timing.json'
Write-Host 'Read-only database-host network timing; about 30 seconds. No setup, uploads or SQL.'
$result = Get-Content -Raw -LiteralPath $source | ssh -o ServerAliveInterval=15 -o ServerAliveCountMax=6 -o StrictHostKeyChecking=yes root@79.143.176.173 'python3 -'
if ($LASTEXITCODE -ne 0) {throw 'Diagnostic SSH failed; no setup attempted.'}
$text = $result -join "`n"
$null = $text | ConvertFrom-Json
[IO.File]::WriteAllText($output,$text,(New-Object System.Text.UTF8Encoding($false)))
Write-Host 'Saved. Tell Codex done; no need to paste the report.'
