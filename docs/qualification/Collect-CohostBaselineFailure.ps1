$ErrorActionPreference = 'Stop'
$workspace = 'C:\Users\EMMA\Desktop\gouwu\qualification'
$source = Join-Path $workspace 'diagnose_cohost_baseline_readonly.py'
$output = Join-Path $workspace 'cohost-baseline-failure-readonly.json'
Write-Host 'Read-only main-server check. Enter the SSH password locally. No uploads, setup or load.'
$result = Get-Content -Raw -LiteralPath $source | ssh -o StrictHostKeyChecking=yes root@173.212.219.169 python3 -
if ($LASTEXITCODE -ne 0) {throw 'Read-only SSH check failed; no setup was attempted.'}
$text = $result -join "`n"
$null = $text | ConvertFrom-Json
[IO.File]::WriteAllText($output,$text,(New-Object System.Text.UTF8Encoding($false)))
Write-Host 'Saved. Tell Codex done; no need to paste the report.'
