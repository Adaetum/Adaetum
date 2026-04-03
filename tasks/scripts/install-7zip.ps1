param()

$ErrorActionPreference = "Stop"

function Get-SevenZipPath {
  $cmd = Get-Command 7z -ErrorAction SilentlyContinue
  if ($cmd -and $cmd.Source) {
    return $cmd.Source
  }

  $direct = Join-Path $env:ProgramFiles "7-Zip\\7z.exe"
  if (Test-Path -LiteralPath $direct) {
    return $direct
  }

  $wingetInstalled = Get-ChildItem -Path (Join-Path $env:LocalAppData "Microsoft\\WinGet\\Packages") -Filter 7z.exe -Recurse -ErrorAction SilentlyContinue | Select-Object -First 1
  if ($wingetInstalled -and $wingetInstalled.FullName) {
    return $wingetInstalled.FullName
  }

  return $null
}

$existing = Get-SevenZipPath
if ($existing) {
  exit 0
}

$winget = Get-Command winget -ErrorAction SilentlyContinue
if (-not $winget) {
  Write-Error "7-Zip is missing and winget is not available. Install manually from https://www.7-zip.org/"
  exit 1
}

winget install --id 7zip.7zip -e --accept-package-agreements --accept-source-agreements --silent | Out-Null

# Refresh PATH for current process after winget install.
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
$machinePath = [Environment]::GetEnvironmentVariable("Path", "Machine")
$env:Path = "$machinePath;$userPath"

$installed = Get-SevenZipPath
if ($installed) {
  Write-Output $installed
  exit 0
}

Write-Error "7-Zip install attempted but executable was not found."
exit 1
