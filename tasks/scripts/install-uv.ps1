param()

$ErrorActionPreference = "Stop"

$py = Get-Command python3 -ErrorAction SilentlyContinue
if (-not $py) {
  $py = Get-Command python -ErrorAction SilentlyContinue
}
if (-not $py) {
  Write-Error "python3 is required for setup tasks. Install Python, then re-run."
  exit 1
}

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

Write-Warning "uv install attempted, but executable was not found on PATH in this session. Continuing; setup scripts will use python fallback."
exit 0
