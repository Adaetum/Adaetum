param()

$ErrorActionPreference = "Stop"
$env:Path = @([Environment]::GetEnvironmentVariable('Path', 'Machine'), [Environment]::GetEnvironmentVariable('Path', 'User'), "$env:UserProfile\.local\bin", "$env:UserProfile\.cargo\bin", $env:Path) -join ';'

# uv is a standalone executable; Python is prepared separately for setup.
$uv = Get-Command uv -ErrorAction SilentlyContinue
if ($uv) {
  exit 0
}

$winget = Get-Command winget -ErrorAction SilentlyContinue
if (-not $winget) {
  Write-Error "uv is missing and winget is not available. Install uv manually: https://docs.astral.sh/uv/getting-started/installation/"
  exit 1
}

winget install --id astral-sh.uv -e --accept-package-agreements --accept-source-agreements --silent | Out-Null
if ($LASTEXITCODE -ne 0) { throw "uv installation failed (winget exit $LASTEXITCODE)." }

$machinePath = [System.Environment]::GetEnvironmentVariable("Path", "Machine")
$userPath = [System.Environment]::GetEnvironmentVariable("Path", "User")
$mergedPath = @($machinePath, $userPath) -join ";"
if (-not [string]::IsNullOrWhiteSpace($mergedPath)) {
  $env:Path = $mergedPath
}

$uv = Get-Command uv -ErrorAction SilentlyContinue
if ($uv) {
  Write-Output ("uv installed: " + $uv.Source)
  exit 0
}

$candidates = @(
  (Join-Path $env:UserProfile ".cargo\bin\uv.exe"),
  (Join-Path $env:UserProfile ".local\bin\uv.exe")
)
foreach ($c in $candidates) {
  if (Test-Path -LiteralPath $c) {
    Write-Output ("uv installed at " + $c + " (restart terminal if not on PATH).")
    exit 0
  }
}

throw "uv was installed but could not be found for this setup session."
