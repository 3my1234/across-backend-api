$ErrorActionPreference='Stop'
$folder='C:\Users\EMMA\Desktop\gouwu\qualification'
Write-Host 'Read-only database-host probe comparison, about one minute. No setup or load test.'
$result=Get-Content -Raw -LiteralPath (Join-Path $folder 'diagnose_parallel_site_probe.py') | ssh -o ServerAliveInterval=15 -o ServerAliveCountMax=6 -o StrictHostKeyChecking=yes root@79.143.176.173 'python3 -'
if($LASTEXITCODE -ne 0){throw 'Diagnostic SSH failed; no test resources started.'}
$text=$result -join "`n"
$null=$text|ConvertFrom-Json
[IO.File]::WriteAllText((Join-Path $folder 'parallel-site-probe-database.json'),$text,(New-Object System.Text.UTF8Encoding($false)))
Write-Host 'Saved. Tell Codex done; no need to paste the report.'
