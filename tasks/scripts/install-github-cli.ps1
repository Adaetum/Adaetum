# Installs GitHub CLI so task init can authenticate in the browser and create
# the user's fork without asking them to manually copy a repository URL.

$ErrorActionPreference = "Stop"

if (Get-Command gh -ErrorAction SilentlyContinue) {
    exit 0
}

if (-not (Get-Command winget -ErrorAction SilentlyContinue)) {
    Write-Error "GitHub CLI is required for task init. Install it from https://cli.github.com/, then rerun task init."
}

Write-Host "GitHub CLI not found. Installing with winget..."
winget install --id GitHub.cli -e --accept-package-agreements --accept-source-agreements

if (-not (Get-Command gh -ErrorAction SilentlyContinue)) {
    Write-Error "GitHub CLI was installed but is not available on PATH. Open a new terminal and rerun task init."
}
