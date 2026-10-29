$ErrorActionPreference = 'Stop'
$collectorPath = Join-Path $PSScriptRoot 'assess_existing_vps_readonly.py'
$captureStamp = [DateTime]::UtcNow.ToString('yyyyMMddTHHmmssZ')
$captureDirectory = Join-Path $PSScriptRoot ('existing-vps-assessment-20261029\' + $captureStamp)
New-Item -ItemType Directory -Path $captureDirectory -Force | Out-Null
foreach ($assessmentTarget in @(@{ Role='main'; Address='173.212.219.169' }, @{ Role='database'; Address='79.143.176.173' })) {
    Write-Host ('Read-only ' + $assessmentTarget.Role + ' observation. Enter the SSH password locally; allow about one minute.')
    $reportLines = @(Get-Content -Raw -LiteralPath $collectorPath | & ssh -o ConnectTimeout=15 ('root@' + $assessmentTarget.Address) python3 - --role $assessmentTarget.Role --seconds 60)
    $sshExit = $LASTEXITCODE
    if (-not $reportLines) { throw ('No report returned for ' + $assessmentTarget.Role) }
    $reportText = $reportLines -join [Environment]::NewLine
    $reportObject = $reportText | ConvertFrom-Json
    $reportFile = Join-Path $captureDirectory ($assessmentTarget.Role + '.json')
    [IO.File]::WriteAllText($reportFile, $reportText, (New-Object Text.UTF8Encoding($false)))
    Write-Host ('Saved ' + $assessmentTarget.Role + ': ' + $reportObject.collection + ' (SSH exit ' + $sshExit + ')')
}
[IO.File]::WriteAllText((Join-Path $PSScriptRoot 'existing-vps-assessment-latest.txt'), $captureDirectory, (New-Object Text.UTF8Encoding($false)))
Write-Host 'Finished. Tell Codex it is done; the reports are saved locally and do not need to be pasted.'
