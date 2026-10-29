$ErrorActionPreference = 'Stop'
$folder='C:\Users\EMMA\Desktop\gouwu\qualification'
foreach ($role in @('main','database')) {
    $address=if($role -eq 'main'){'173.212.219.169'}else{'79.143.176.173'}
    Write-Host "Read-only $role site timing (about 30 seconds). Enter SSH password locally. No setup."
    $result=Get-Content -Raw -LiteralPath (Join-Path $folder 'diagnose_protected_site_path.py') | ssh -o ServerAliveInterval=15 -o ServerAliveCountMax=6 -o StrictHostKeyChecking=yes "root@$address" "python3 - --role $role"
    if($LASTEXITCODE -ne 0){throw "$role diagnostic SSH failed; no resources started."}
    $text=$result -join "`n"
    $null=$text|ConvertFrom-Json
    [IO.File]::WriteAllText((Join-Path $folder "protected-site-timing-$role.json"),$text,(New-Object System.Text.UTF8Encoding($false)))
}
Write-Host 'Both timing reports saved. Tell Codex done; no need to paste them.'
