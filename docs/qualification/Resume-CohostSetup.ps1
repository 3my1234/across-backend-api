param([switch]$ContinueStaged)
$ErrorActionPreference = 'Stop'
$workspace = 'C:\Users\EMMA\Desktop\gouwu'
$run = 'cohost-2f12dc3ecbf8acb1'
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
    Native -exe 'ssh' -arguments @('-o','ServerAliveInterval=15','-o','ServerAliveCountMax=6','-o','ConnectTimeout=20','-o','StrictHostKeyChecking=yes',"root@$address",$command)
}
if ((Get-FileHash -Algorithm SHA256 $bundle).Hash.ToLowerInvariant() -ne '020bacefeffd12bdfa460c165b43b471f9f987b5da7579bdfd219137ceb2c366') { throw 'Image archive checksum mismatch' }
Write-Host 'Approved SETUP ONLY resume. Reusing both uploaded image archives; no image uploads. Enter passwords locally.'
# Retain the DB host key already obtained through the trusted authenticated SSH session.
if (-not (Test-Path -LiteralPath (Join-Path $review 'main\database_known_hosts'))) { throw 'Trusted DB host-key pin is missing; stop.' }
$utf8 = New-Object System.Text.UTF8Encoding($false)
if (-not $ContinueStaged) {
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
    Remote $address "if [ ! -d '$remote' ]; then if [ ! -f '/root/atlxpres-cohost/cohost-6d6c5f9220874022/cleanup.complete' ]; then echo 'STOP: previous attempt cleanup marker missing' >&2; exit 1; fi; if [ -L '/root/atlxpres-cohost/cohost-6d6c5f9220874022/images.tar.gz' ]; then echo 'STOP: source archive is a symlink' >&2; exit 1; fi; mkdir -m 700 '$remote' && ln '/root/atlxpres-cohost/cohost-6d6c5f9220874022/images.tar.gz' '$remote/images.tar.gz' || exit 1; fi; cd '$remote' || exit 1; if [ -e manifest.json ]; then echo 'STOP: this attempt already has a setup manifest; refusing restage' >&2; exit 1; fi; if [ ! -f images.tar.gz ]; then echo 'STOP: staged image archive missing' >&2; exit 1; fi; echo '020bacefeffd12bdfa460c165b43b471f9f987b5da7579bdfd219137ceb2c366  images.tar.gz' | sha256sum -c -"
    Native -exe 'scp' -arguments @('-o','StrictHostKeyChecking=yes',$archive,"root@${address}:$remote/setup.tar")
    Remove-Item -LiteralPath $archive
    Remote $address "umask 077; cd '$remote' && tar -xf setup.tar && chmod -R go-rwx '$remote' && rm setup.tar"
}
} else {
    foreach ($role in @('main','database')) {
        $request = Get-Content -Raw -LiteralPath (Join-Path $review "$role\request.json") | ConvertFrom-Json
        $deadline = [DateTime]::ParseExact($request.key_expiry_utc,'yyyyMMddHHmmss\Z',$null)
        if ($deadline -lt [DateTime]::UtcNow.AddMinutes(30)) { throw 'Staged key expiry is too close; stop for a small-package refresh.' }
    }
    Write-Host 'Continuing staged packages. No uploads. Fresh completed baselines will be reused.'
}
# Reuse a valid completed baseline. Wait for a disconnected collector if present;
# never start a second collector while the existing one is still running.
Remote '173.212.219.169' "python3 '$remote/tools/stage_host.py' '$remote' --preflight"
Remote '79.143.176.173' "python3 '$remote/tools/stage_host.py' '$remote' --preflight"
$baselineCode = @'
import json,sys,time,subprocess
from pathlib import Path
base=Path('/root/atlxpres-cohost/cohost-2f12dc3ecbf8acb1')
sys.path.insert(0,str(base/'tools'))
from baseline import validate
if (base/'manifest.json').exists():raise RuntimeError('Setup already started; do not restage')
def fresh():
    try:validate(json.loads((base/'baseline.json').read_text()),time.time());return True
    except (ValueError,OSError):return False
if not fresh():
    deadline=time.monotonic()+420
    while True:
        active=False
        for path in Path('/proc').glob('[0-9]*/cmdline'):
            try:active=active or str(base/'tools/baseline.py').encode() in path.read_bytes().split(b'\0')
            except OSError:pass
        if not active:break
        if time.monotonic()>deadline:raise RuntimeError('Previous collector still running; no duplicate started')
        time.sleep(2)
    if not fresh():subprocess.run(['python3',str(base/'tools/operator_setup.py'),str(base),'baseline'],check=True,timeout=420)
print('Fresh baseline validated; no load authorized.')
'@
function RemoteBaseline([string]$address) {
    # PowerShell 5.1 strips nested native-command quotes. Send ASCII Python on
    # stdin instead; the remote shell sees only the fixed command python3 -.
    $baselineCode | & ssh -o ServerAliveInterval=15 -o ServerAliveCountMax=6 -o ConnectTimeout=20 -o StrictHostKeyChecking=yes "root@$address" 'python3 -'
    if ($LASTEXITCODE -ne 0) { throw "Baseline SSH failed ($LASTEXITCODE); setup stopped." }
}
RemoteBaseline '173.212.219.169'
RemoteBaseline '79.143.176.173'
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
            Remote $address "cd '$remote' && tar -cf evidence.tar --ignore-failed-read request.json manifest.json compose.runtime.json image-identities.json image-import.log baseline.json baseline-failure.json sample.json watchdog.jsonl verification.complete cleanup.complete"
            Native -exe 'scp' -arguments @('-o','StrictHostKeyChecking=yes',"root@${address}:$remote/evidence.tar",(Join-Path $evidence "$role-evidence.tar"))
        } catch { Write-Warning "${role}: $($_.Exception.Message). Independent cleanup task remains responsible for this run." }
    }
}
Write-Host "Finished. Tell Codex; evidence is saved in $evidence. No load test was started."
