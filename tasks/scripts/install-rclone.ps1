param()

$ErrorActionPreference = "Stop"

$rclone = Get-Command rclone -ErrorAction SilentlyContinue
if ($rclone) {
  exit 0
}

$wingetInstalled = Get-ChildItem -Path (Join-Path $env:LocalAppData "Microsoft\WinGet\Packages") -Filter rclone.exe -Recurse -ErrorAction SilentlyContinue | Select-Object -First 1
if ($wingetInstalled -and $wingetInstalled.FullName) {
  $toolDir = Split-Path -Parent $wingetInstalled.FullName
  $env:Path = "$toolDir;$env:Path"
  exit 0
}

$winget = Get-Command winget -ErrorAction SilentlyContinue
if (-not $winget) {
  Write-Error "rclone is missing and winget is not available. Install rclone manually: https://rclone.org/install/"
  exit 1
}

Write-Output "rclone not found. Installing via winget..."
winget install --id Rclone.Rclone -e --accept-package-agreements --accept-source-agreements --silent | Out-Null

# Refresh PATH for current process after winget install.
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
$machinePath = [Environment]::GetEnvironmentVariable("Path", "Machine")
$env:Path = "$machinePath;$userPath"

$rclone = Get-Command rclone -ErrorAction SilentlyContinue
if ($rclone) {
  Write-Output ("rclone installed: " + $rclone.Source)
  exit 0
}

$candidates = @(
  (Join-Path $env:ProgramFiles "rclone\rclone.exe"),
  (Join-Path $env:LocalAppData "Programs\rclone\rclone.exe")
)

foreach ($c in $candidates) {
  if (Test-Path -LiteralPath $c) {
    Write-Output ("rclone installed at " + $c + " (restart terminal if not on PATH).")
    exit 0
  }
}

Write-Error "rclone install attempted but executable was not found on PATH."
exit 1
