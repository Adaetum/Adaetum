param()

$ErrorActionPreference = "Stop"

$profilePath = $PROFILE
$markerStart = "# >>> task completion >>>"
$markerEnd = "# <<< task completion <<<"
$completionBlock = @"
# >>> task completion >>>
task --completion powershell | Out-String | Invoke-Expression
# <<< task completion <<<
"@

if (-not (Test-Path -LiteralPath $profilePath)) {
  New-Item -ItemType File -Force -Path $profilePath | Out-Null
}

$profileText = Get-Content -LiteralPath $profilePath -Raw -ErrorAction SilentlyContinue
$replacementPattern = "(?ms)^\Q$markerStart\E\r?\n.*?^\Q$markerEnd\E\r?\n?"
$updated = $false

if ([string]::IsNullOrEmpty($profileText)) {
  Set-Content -LiteralPath $profilePath -Value $completionBlock
  $updated = $true
} elseif ($profileText -match $replacementPattern) {
  $newText = [regex]::Replace($profileText, $replacementPattern, $completionBlock + "`r`n")
  if ($newText -ne $profileText) {
    Set-Content -LiteralPath $profilePath -Value $newText
    $updated = $true
  }
} elseif ($profileText -notmatch [regex]::Escape($markerStart)) {
  if (-not $profileText.EndsWith("`n")) {
    $profileText += "`r`n"
  }
  Set-Content -LiteralPath $profilePath -Value ($profileText + $completionBlock)
  $updated = $true
}

if ($updated) {
  Write-Output "PowerShell task completion installed or updated."
  Write-Output "Load it in this shell with: . `$PROFILE"
  Write-Output "If VS Code still does not pick it up, open a new terminal."
}
