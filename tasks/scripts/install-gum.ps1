# Installs the optional interactive presenter used only by `task init`.
# The setup flow itself still supports plain terminal prompts through `task initialize`.

$ErrorActionPreference = "Stop"

if (Get-Command gum -ErrorAction SilentlyContinue) {
    exit 0
}

if (-not (Get-Command winget -ErrorAction SilentlyContinue)) {
    Write-Error "Gum is required for task init. Install it from https://github.com/charmbracelet/gum#installation, then rerun task init."
}

Write-Host "Gum not found. Installing with winget..."
winget install --id charmbracelet.gum -e --accept-package-agreements --accept-source-agreements

if (-not (Get-Command gum -ErrorAction SilentlyContinue)) {
    Write-Error "Gum was installed but is not available on PATH. Open a new terminal and rerun task init."
}
