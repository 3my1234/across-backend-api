$ErrorActionPreference = 'Stop'
$workspace = 'C:\Users\EMMA\Desktop\gouwu'
$run = 'cohost-6d6c5f9220874022'
$review = Join-Path $workspace "qualification\cohost-review\$run"
$private = Join-Path $workspace "qualification\cohost-private\$run"
$remote = "/root/atlxpres-cohost/$run"
$bundle = Join-Path $workspace 'qualification\cohost-image-bundle-40930181592\images.tar.gz'
$evidence = Join-Path $workspace "qualification\cohost-runtime-$run"
New-Item -ItemType Directory -Force $evidence | Out-Null
function Native([string]$exe, [Parameter(ValueFromRemainingArguments=$true)][string[]]$arguments) {
    & $exe @arguments
    if ($LASTEXITCODE -ne 0) { throw "$exe failed ($LASTEXITCODE); setup stopped." }
}
function Remote([string]$address, [string]$command) {
    Native -exe 'ssh' -arguments @('-o','StrictHostKeyChecking=yes',"root@$address",$command)
}
if ((Get-FileHash -Algorithm SHA256 $bundle).Hash.ToLowerInvariant() -ne '020bacefeffd12bdfa460c165b43b471f9f987b5da7579bdfd219137ceb2c366') { throw 'Image archive checksum mismatch' }
Write-Host 'Approved SETUP ONLY resume. Reusing both uploaded image archives; no image uploads. Enter passwords locally.'
# Retain the DB host key already obtained through the trusted authenticated SSH session.
if (-not (Test-Path -LiteralPath (Join-Path $review 'main\database_known_hosts'))) { throw 'Trusted DB host-key pin is missing; stop.' }
$utf8 = New-Object System.Text.UTF8Encoding($false)
$expiry = [DateTime]::UtcNow.AddHours(3).ToString('yyyyMMddHHmmss') + 'Z'
# Refresh only uninstalled temporary key expiry within the existing setup-only approval.
foreach ($role in @('main','database')) {
    $rolePath = Join-Path $review $role
    $requestPath = Join-Path $rolePath 'request.json'
    $request = Get-Content -Raw -LiteralPath $requestPath | ConvertFrom-Json
    $approvalPath = Join-Path $rolePath 'approval.json'
    $approval = Get-Content -Raw -LiteralPath $approvalPath | ConvertFrom-Json
    if ($request.run -ne $run -or $request.role -ne $role -or $approval.run -ne $run -or $approval.authorized_setup -ne $true -or $approval.load_authorized -ne $false) { throw 'Setup approval mismatch' }
    if ((Get-FileHash -Algorithm SHA256 (Join-Path $rolePath 'compose.json')).Hash.ToLowerInvariant() -ne $request.compose_sha256) { throw 'Reviewed compose changed' }
    $request.key_expiry_utc = $expiry
    $approval.approved_unix = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
    [IO.File]::WriteAllText($requestPath,($request | ConvertTo-Json -Depth 12),$utf8)
    [IO.File]::WriteAllText($approvalPath,($approval | ConvertTo-Json -Depth 12),$utf8)
}
foreach ($role in @('main','database')) {
    $address = if ($role -eq 'main') {'173.212.219.169'} else {'79.143.176.173'}
    $rolePath = Join-Path $review $role
    $archive = Join-Path $private "$role-setup.tar"
    if ($role -eq 'main') { Copy-Item -LiteralPath (Join-Path $private 'main-database') -Destination (Join-Path $rolePath 'main-database.key') }
    try { Native -exe 'tar' -arguments @('-cf',$archive,'-C',$rolePath,'.') }
    finally { Remove-Item -LiteralPath (Join-Path $rolePath 'main-database.key') -ErrorAction SilentlyContinue }
    # Binary-safe SCP; never pipe an image archive through Windows PowerShell.
    Remote $address "cd '$remote' && test ! -e manifest.json && test -f request.json && echo '020bacefeffd12bdfa460c165b43b471f9f987b5da7579bdfd219137ceb2c366  images.tar.gz' | sha256sum -c -"
    Native -exe 'scp' -arguments @('-o','StrictHostKeyChecking=yes',$archive,"root@${address}:$remote/setup.tar")
    Remove-Item -LiteralPath $archive
    Remote $address "umask 077; cd '$remote' && tar -xf setup.tar && chmod -R go-rwx '$remote' && rm setup.tar"
}
# Both uploads finish BEFORE either short-lived setup guardian starts.
Remote '173.212.219.169' "python3 '$remote/tools/operator_setup.py' '$remote' baseline"
Remote '79.143.176.173' "python3 '$remote/tools/operator_setup.py' '$remote' baseline"
try {
    Remote '79.143.176.173' "python3 '$remote/tools/operator_setup.py' '$remote' stage"
    Remote '173.212.219.169' "python3 '$remote/tools/operator_setup.py' '$remote' stage"
    Remote '173.212.219.169' "python3 '$remote/tools/operator_setup.py' '$remote' verify"
    foreach ($role in @('main','database')) {
        $address = if ($role -eq 'main') {'173.212.219.169'} else {'79.143.176.173'}
        $key = Join-Path $private "ci-$role"
        Native -exe 'ssh' -arguments @('-o','BatchMode=yes','-o','StrictHostKeyChecking=yes','-o','IdentitiesOnly=yes','-i',$key,"root@$address",'status')
    }
    Write-Host 'Setup and restricted-key connectivity passed. No load started. Removing disposable test resources.'
} finally {
    foreach ($role in @('main','database')) {
        $address = if ($role -eq 'main') {'173.212.219.169'} else {'79.143.176.173'}
        try {
            Remote $address "python3 '$remote/tools/operator_setup.py' '$remote' cleanup"
            # Explicit public metadata only; NEVER copy the private tunnel key.
            Remote $address "cd '$remote' && tar -cf evidence.tar --ignore-failed-read request.json manifest.json baseline.json baseline-failure.json sample.json watchdog.jsonl verification.complete cleanup.complete"
            Native -exe 'scp' -arguments @('-o','StrictHostKeyChecking=yes',"root@${address}:$remote/evidence.tar",(Join-Path $evidence "$role-evidence.tar"))
        } catch { Write-Warning "${role}: $($_.Exception.Message). Independent cleanup task remains responsible for this run." }
    }
}
Write-Host "Finished. Tell Codex; evidence is saved in $evidence. No load test was started."
