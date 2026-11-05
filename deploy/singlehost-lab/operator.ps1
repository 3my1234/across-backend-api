param([Parameter(Mandatory=$true)][string]$Review,[switch]$ApproveDatabaseHostPlacement)
if (-not $ApproveDatabaseHostPlacement) { throw 'APPROVAL REQUIRED: database VPS placement capped at3CPU/5.5GiB; no main-VPS test resources. Review the execution package first.' }
$ErrorActionPreference='Stop'
$repo='3my1234/across-backend-api'
$source=Split-Path (Split-Path $PSScriptRoot)
function Native([string]$exe,[Parameter(ValueFromRemainingArguments=$true)][string[]]$arguments) {
    & $exe @arguments
    if ($LASTEXITCODE -ne 0) { throw "$exe failed ($LASTEXITCODE); no automatic retry" }
}
$a=Get-Content -Raw -LiteralPath (Join-Path $Review 'approval.json') | ConvertFrom-Json
$run=$a.run
if ($run -notmatch '^cohost-[a-f0-9]{16}$' -or $a.authorized -ne $false) { throw 'Fresh unauthorized review required' }
$head=& git -C $source rev-parse HEAD
if ($LASTEXITCODE -ne 0 -or $head -ne $a.source_commit) { throw 'Source changed since review' }
$proof=& gh run view "$($a.rehearsal_run)" --repo $repo --json status,conclusion,headSha,jobs | ConvertFrom-Json
if ($LASTEXITCODE -ne 0 -or $proof.status -ne 'completed' -or $proof.conclusion -ne 'success' -or $proof.headSha -ne $head) { throw 'Exact-source disposable rehearsal has not passed' }
$fullReplay=@($proof.jobs | Where-Object { $_.name -eq 'rehearsal' -and $_.status -eq 'completed' -and $_.conclusion -eq 'success' })
if ($fullReplay.Count -ne 1) { throw 'A network-only or policy-only result is not a full rehearsal' }
if ($a.execution_ref -ne "experiment/approved-singlehost-$($head.Substring(0,7))") { throw 'Frozen execution reference mismatch' }
$published=& gh api "repos/$repo/git/ref/heads/$($a.execution_ref)" --jq '.object.sha'
if ($LASTEXITCODE -ne 0 -or $published -ne $head) { throw 'Frozen execution source is not published' }
$secretNames=& gh secret list --repo $repo --json name --jq '.[].name'
if ($LASTEXITCODE -ne 0 -or $secretNames -contains 'SINGLEHOST_TRIAL_BUNDLE') { throw 'Cannot safely install a unique temporary CI secret' }
# Only prior test ownership is inspected on main. No site, production SQL or setup.
$audit=@'
import json,subprocess
from pathlib import Path
run='cohost-56ee8ed93722432b'
base=Path('/root/atlxpres-singlehost')/run
if not (base/'cleanup.finalized').is_file():raise RuntimeError('Previous cleanup marker missing')
ids=subprocess.check_output(['docker','ps','-aq','--filter','label=atlxpres.cohost.run='+run],text=True,timeout=15).strip()
if ids:raise RuntimeError('Previous test containers remain')
keys=Path('/root/.ssh/authorized_keys')
if keys.exists() and any(run in row for row in keys.read_text().splitlines()):raise RuntimeError('Previous temporary key remains')
print(json.dumps(dict(previous_run=run,remaining_containers=0,remaining_keys=0,read_only=True)))
'@
foreach($address in @('79.143.176.173')) {
    $audit | & ssh -o StrictHostKeyChecking=yes -o ConnectTimeout=20 "root@$address" 'python3 -'
    if ($LASTEXITCODE -ne 0) { throw 'Previous cleanup not confirmed; no new setup started' }
}
$packed=& python (Join-Path $PSScriptRoot 'pack.py') $Review --approve-placement | ConvertFrom-Json
if ($LASTEXITCODE -ne 0) { throw 'Reviewed package could not be frozen' }
$a=Get-Content -Raw -LiteralPath (Join-Path $Review 'approval.json') | ConvertFrom-Json
$private=$packed.private
$known=(& ssh-keygen -F '79.143.176.173' -f (Join-Path $env:USERPROFILE '.ssh/known_hosts')) -join "`n"
if ($LASTEXITCODE -ne 0 -or -not $known) { throw 'Previously trusted database host key missing' }
$knownPath=Join-Path $private 'known_hosts'
[IO.File]::WriteAllText($knownPath,$known+"`n",(New-Object Text.UTF8Encoding($false)))
$remotePackage="/root/$run-singlehost.tar.gz"
Write-Host 'Uploading reviewed tools and reusable synthetic database snapshot. Existing images are reused. Enter the database-host password locally.'
Native -exe 'scp' -arguments @('-o','StrictHostKeyChecking=yes',$packed.package,"root@79.143.176.173:$remotePackage")
Get-Content -Raw -LiteralPath (Join-Path $Review 'install_host.py') | & ssh -o StrictHostKeyChecking=yes -o ConnectTimeout=20 root@79.143.176.173 "python3 - --package $remotePackage --sha256 $($packed.sha256) --run $run"
if ($LASTEXITCODE -ne 0) { throw 'Single-host installation did not finish; do not rerun. Inspect this exact job and cleanup first.' }
function Trial([string]$command) {
    $output=& python (Join-Path $PSScriptRoot 'ssh_once.py') --private $private --command $command
    if ($LASTEXITCODE -ne 0) { throw "Restricted test command failed: $command. Server-side hard stop remains armed." }
    return $output
}
$installedSecret=$false
try {
    $deadline=[DateTime]::UtcNow.AddMinutes(20)
    while ($true) {
        $status=Trial 'setup-status' | ConvertFrom-Json
        Write-Host "Single-host setup: phase=$($status.phase), ready=$($status.ready), closed=$($status.closed)"
        if ($status.closed -or $status.guard_error) { throw 'Single-host setup aborted; no workload dispatch' }
        if ($status.ready) { break }
        if ([DateTime]::UtcNow -gt $deadline) { throw 'Bounded setup wait expired' }
        Start-Sleep -Seconds 15
    }
    $bundle=@{approval=$a;key=[IO.File]::ReadAllText((Join-Path $private 'ci'));known_hosts=$known}
    $bundleJson=$bundle | ConvertTo-Json -Depth 12 -Compress
    if ($bundleJson.Length -ge 48000) { throw 'Temporary credential envelope too large' }
    $bundleJson | & gh secret set SINGLEHOST_TRIAL_BUNDLE --repo $repo
    if ($LASTEXITCODE -ne 0) { throw 'Temporary secret upload failed' }
    $installedSecret=$true
    Native -exe 'gh' -arguments @('workflow','run','singlehost-rehearsal.yml','--repo',$repo,'--ref',$a.execution_ref,'-f','execute=true','-f',"run_nonce=$run")
    $action=$null
    for($poll=0;$poll -lt 18;$poll++) {
        $runs=& gh run list --repo $repo --workflow singlehost-rehearsal.yml --limit 10 --json databaseId,displayTitle | ConvertFrom-Json
        if ($LASTEXITCODE -ne 0) { throw 'Cannot identify dispatched job' }
        $action=$runs | Where-Object { $_.displayTitle -eq "Single-host trial $run" } | Select-Object -First 1
        if ($action) { break }
        Start-Sleep -Seconds 5
    }
    if (-not $action) { throw 'Dispatched trial did not appear; no duplicate dispatch' }
    Write-Host "Trial: https://github.com/$repo/actions/runs/$($action.databaseId)"
    Write-Host 'GitHub watch shows step status, not streamed logs. Open the link for30-second phase heartbeats. The workload includes30minutes at500 sessions plus receipt verification; hard limit120minutes. Do not start another copy.'
    Native -exe 'gh' -arguments @('run','watch',"$($action.databaseId)",'--repo',$repo,'--exit-status','--interval','15')
} finally {
    $bundle=$null;$bundleJson=$null
    if ($installedSecret) { & gh secret delete SINGLEHOST_TRIAL_BUNDLE --repo $repo }
    try { Trial 'cleanup' | Write-Host } catch { Write-Warning 'Restricted cleanup confirmation unavailable; inspect the exact server-side job. Its watchdog and120-minute hard stop remain independent.' }
}
