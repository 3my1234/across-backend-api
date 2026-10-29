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
if ([DateTime]::UtcNow -gt [DateTime]::ParseExact('20261029202724','yyyyMMddHHmmss',$null)) {
    throw 'Not enough time remains on the reviewed keys. Ask Codex to refresh this package before running.'
}
if ((Get-FileHash -Algorithm SHA256 $bundle).Hash.ToLowerInvariant() -ne '020bacefeffd12bdfa460c165b43b471f9f987b5da7579bdfd219137ceb2c366') { throw 'Image archive checksum mismatch' }
Write-Host 'Approved SETUP ONLY. No seeding or load. Enter SSH passwords locally when requested.'
# Read the DB host key through an already trusted, authenticated SSH connection.
$hostKey = & ssh -o StrictHostKeyChecking=yes root@79.143.176.173 'cat /etc/ssh/ssh_host_ed25519_key.pub'
if ($LASTEXITCODE -ne 0 -or $hostKey -notmatch '^ssh-ed25519 [A-Za-z0-9+/=]+') { throw 'Trusted DB host-key retrieval failed' }
$utf8 = New-Object System.Text.UTF8Encoding($false)
[IO.File]::WriteAllText((Join-Path $review 'main\database_known_hosts'),"79.143.176.173 $hostKey`n",$utf8)
foreach ($role in @('main','database')) {
    $address = if ($role -eq 'main') {'173.212.219.169'} else {'79.143.176.173'}
    $rolePath = Join-Path $review $role
    $archive = Join-Path $private "$role-setup.tar"
    if ($role -eq 'main') { Copy-Item -LiteralPath (Join-Path $private 'main-database') -Destination (Join-Path $rolePath 'main-database.key') }
    try { Native -exe 'tar' -arguments @('-cf',$archive,'-C',$rolePath,'.') }
    finally { Remove-Item -LiteralPath (Join-Path $rolePath 'main-database.key') -ErrorAction SilentlyContinue }
    # Binary-safe SCP; never pipe an image archive through Windows PowerShell.
    Remote $address "umask 077; test ! -e '$remote' && mkdir -p '$remote'"
    Native -exe 'scp' -arguments @('-o','StrictHostKeyChecking=yes',$archive,"root@${address}:$remote/setup.tar")
    Native -exe 'scp' -arguments @('-o','StrictHostKeyChecking=yes',$bundle,"root@${address}:$remote/images.tar.gz")
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
            Remote $address "cd '$remote' && tar -cf evidence.tar --ignore-failed-read request.json manifest.json baseline.json sample.json watchdog.jsonl verification.complete cleanup.complete"
            Native -exe 'scp' -arguments @('-o','StrictHostKeyChecking=yes',"root@${address}:$remote/evidence.tar",(Join-Path $evidence "$role-evidence.tar"))
        } catch { Write-Warning "${role}: $($_.Exception.Message). Independent cleanup task remains responsible for this run." }
    }
}
Write-Host "Finished. Tell Codex; evidence is saved in $evidence. No load test was started."
