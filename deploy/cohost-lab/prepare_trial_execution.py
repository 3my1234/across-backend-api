"""Prepare local review files ONLY. No SSH, GitHub mutation, load or approval."""
import argparse,json,shutil,subprocess,sys
from pathlib import Path
from trial_policy import STAGES,LIFETIME,client_hashes

def prepare(workspace,safety_run):
    if not str(safety_run).isdigit():raise ValueError('exact passed disposable safety run required')
    root=Path(__file__).resolve().parents[2]
    source=subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip()
    status=subprocess.check_output(['git','status','--porcelain'],cwd=root,text=True)
    if status.strip():raise ValueError('commit reviewed source before preparing exact trial')
    args=[sys.executable,str(root/'deploy/cohost-lab/prepare_review.py'),str(workspace/'qualification/cohost-image-bundle-40930181592'),str(workspace/'qualification/cohost-review'),str(workspace/'qualification/cohost-private')]
    generated=json.loads(subprocess.check_output(args,cwd=root,text=True).splitlines()[-1])
    review=Path(generated['review']).resolve();run=review.name;private=Path(generated['private_keys_location'])
    prior=workspace/'qualification/cohost-review/cohost-0cfe085cbad1ec57/main/database_known_hosts'
    shutil.copyfile(prior,review/'main/database_known_hosts')
    trial=dict(run=run,load_authorized=False,fixture_preparation_authorized=False,spending_cap_usd=0,
        main_cpu=2,main_memory=2684354560,database_cpu=3,database_memory=3221225472,
        generator_cpu=2,generator_memory=4294967296,lifetime_seconds=LIFETIME,approved_unix=0,
        source_commit=source,safety_run=str(safety_run),image_source=json.loads((review/'main/request.json').read_text())['image_source'],
        stages=[list(s) for s in STAGES],client_hashes=client_hashes(root))
    for role in ('main','database'):
        shutil.copyfile(review/role/'approval.template.json',review/role/'approval.json')
        (review/role/'trial-approval.json').write_text(json.dumps(trial,indent=2))
    (review/'trial-review.json').write_text(json.dumps(trial,indent=2))
    template=(workspace/'qualification/Resume-CohostSetup.ps1').read_text(encoding='utf-8-sig')
    template=template.replace("param([switch]$ContinueStaged)","param([switch]$ApproveTrial)\nif (-not $ApproveTrial) { throw 'APPROVAL REQUIRED: review the exact trial plan before using -ApproveTrial.' }\n$ContinueStaged = $false")
    template=template.replace('cohost-0cfe085cbad1ec57',run)
    template=template.replace("Write-Host 'Approved SETUP ONLY resume. Reusing both uploaded image archives; no image uploads. Enter passwords locally.'", "Write-Host 'EXPLICITLY APPROVED TRIAL: fresh isolated instance, small tools only, existing images reused. No production changes.'")
    anchor="if ((Get-FileHash -Algorithm SHA256 $bundle)"
    pos=template.index(anchor)
    initialize="""
$repo = '3my1234/across-backend-api'
$localSource = 'SOURCE_COMMIT'
$secretPresent = & gh secret list --repo $repo --json name --jq '.[].name'
if ($LASTEXITCODE -ne 0) { throw 'Cannot check temporary secret name' }
if ($secretPresent -contains 'COHOST_TRIAL_BUNDLE') { throw 'Temporary trial secret already exists; do not overwrite' }
foreach ($role in @('main','database')) {
    $path = Join-Path $review $role
    $a = Get-Content -Raw (Join-Path $path 'approval.json') | ConvertFrom-Json
    $a.authorized_setup = $true
    $a.authorized_image_import = $true
    [IO.File]::WriteAllText((Join-Path $path 'approval.json'),($a | ConvertTo-Json -Depth 12),(New-Object Text.UTF8Encoding($false)))
    $t = Get-Content -Raw (Join-Path $path 'trial-approval.json') | ConvertFrom-Json
    if ($t.source_commit -ne $localSource -or $t.run -ne $run) { throw 'Reviewed trial identity mismatch' }
    $t.load_authorized = $true
    $t.fixture_preparation_authorized = $true
    $t.approved_unix = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
    [IO.File]::WriteAllText((Join-Path $path 'trial-approval.json'),($t | ConvertTo-Json -Depth 12),(New-Object Text.UTF8Encoding($false)))
}
""".replace('SOURCE_COMMIT',source)
    template=template[:pos]+initialize+template[pos:]
    old="    Write-Host 'Setup and restricted-key connectivity passed. No load started. Removing disposable test resources.'"
    execute="""
    # Host setup remains idle until the manually dispatched CI validates both approvals.
    $mainPin = (& ssh-keygen -F '173.212.219.169' -f (Join-Path $env:USERPROFILE '.ssh/known_hosts')) -join "`n"
    if ($LASTEXITCODE -ne 0 -or -not $mainPin) { throw 'Trusted main host-key pin missing' }
    $bundleObject = @{
        approval = (Get-Content -Raw (Join-Path $review 'main/trial-approval.json') | ConvertFrom-Json)
        main_key = Get-Content -Raw (Join-Path $private 'ci-main')
        database_key = Get-Content -Raw (Join-Path $private 'ci-database')
        main_known_hosts = $mainPin
        database_known_hosts = Get-Content -Raw (Join-Path $review 'main/database_known_hosts')
    }
    $secretInstalled = $false
    try {
        $bundleObject | ConvertTo-Json -Depth 20 -Compress | & gh secret set COHOST_TRIAL_BUNDLE --repo $repo
        if ($LASTEXITCODE -ne 0) { throw 'Temporary restricted-key secret upload failed' }
        $secretInstalled = $true
        $liveHead = & gh api 'repos/3my1234/across-backend-api/git/ref/heads/experiment/launch-qualification' --jq '.object.sha'
        if ($LASTEXITCODE -ne 0 -or $liveHead -ne $localSource) { throw 'Published reviewed source changed; do not dispatch' }
        Native -exe 'gh' -arguments @('workflow','run','cohost-preparation.yml','--repo',$repo,'--ref','experiment/launch-qualification','-f','execute=true','-f',"run_nonce=$run")
        $trialAction = $null
        for ($poll=0; $poll -lt 18; $poll++) {
            $runs = & gh run list --repo $repo --workflow cohost-preparation.yml --limit 10 --json databaseId,displayTitle | ConvertFrom-Json
            if ($LASTEXITCODE -ne 0) { throw 'Cannot identify trial workflow' }
            $trialAction = $runs | Where-Object { $_.displayTitle -eq "Cohost trial $run" } | Select-Object -First 1
            if ($trialAction) { break }
            Start-Sleep -Seconds 5
        }
        if (-not $trialAction) { throw 'Trial job did not appear; cleanup without retry' }
        Write-Host "Trial run: https://github.com/$repo/actions/runs/$($trialAction.databaseId)"
        & gh run watch "$($trialAction.databaseId)" --repo $repo --exit-status --interval 15
        $trialExit = $LASTEXITCODE
        Native -exe 'gh' -arguments @('run','download',"$($trialAction.databaseId)",'--repo',$repo,'--name',"cohost-trial-$($trialAction.databaseId)",'--dir',(Join-Path $evidence 'trial-evidence'))
        if ($trialExit -ne 0) { throw "Trial did not pass (exit $trialExit); evidence retained, no escalation" }
    } finally {
        $bundleObject = $null
        if ($secretInstalled) { & gh secret delete COHOST_TRIAL_BUNDLE --repo $repo }
    }
"""
    if old not in template:raise ValueError('reviewed setup template anchor changed')
    template=template.replace(old,execute)
    password_verify='    Remote \'173.212.219.169\' "python3 \'$remote/tools/operator_setup.py\' \'$remote\' verify"'
    key_verify="    Native -exe 'ssh' -arguments @('-o','BatchMode=yes','-o','StrictHostKeyChecking=yes','-o','IdentitiesOnly=yes','-i',(Join-Path $private 'ci-main'),'root@173.212.219.169','setup-verify')"
    if password_verify not in template:raise ValueError('exact verification handoff anchor changed')
    template=template.replace(password_verify,key_verify)
    template=template.replace('No load test was started.','See retained trial result; cleanup evidence is separate from capacity acceptance.')
    script=workspace/'qualification/Invoke-CohostQualification.ps1'
    script.write_text(template,encoding='utf-8-sig')
    (workspace/'qualification/cohost-trial-review-latest.txt').write_text(str(review))
    print(json.dumps(dict(run=run,source=source,review=str(review),operator_command=str(script),approval_required=True,load_started=False)))

if __name__=='__main__':
    ap=argparse.ArgumentParser();ap.add_argument('workspace',type=Path);ap.add_argument('--safety-run',required=True);a=ap.parse_args();prepare(a.workspace.resolve(),a.safety_run)
