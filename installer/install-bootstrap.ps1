$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot

function Refresh-SetupPath {
    $parts = @([Environment]::GetEnvironmentVariable('Path', 'Machine'), [Environment]::GetEnvironmentVariable('Path', 'User'), $env:Path)
    foreach ($candidate in @(
        "$env:ProgramFiles\Git\cmd", "$env:ProgramFiles\Git\bin", "$env:ProgramFiles\Go\bin",
        "$env:ProgramFiles\nodejs", "$env:ProgramFiles\GitHub CLI", "$env:ProgramFiles\7-Zip",
        "$env:LocalAppData\Microsoft\WinGet\Links", "$env:UserProfile\.local\bin", "$env:UserProfile\go\bin"
    )) { if (Test-Path -LiteralPath $candidate) { $parts += $candidate } }
    $pythonRoot = Join-Path $env:LocalAppData 'Programs\Python'
    if (Test-Path -LiteralPath $pythonRoot) {
        Get-ChildItem -LiteralPath $pythonRoot -Directory -Filter 'Python3*' | Sort-Object Name -Descending | ForEach-Object { $parts += $_.FullName; $parts += (Join-Path $_.FullName 'Scripts') }
    }
    $env:Path = ($parts | Where-Object { $_ }) -join ';'
}

function Find-WorkingCommand([string]$name) {
    foreach ($cmd in @(Get-Command $name -All -CommandType Application -ErrorAction SilentlyContinue)) {
        if (-not $cmd.Source) { continue }
        $versionArg = if ($name -eq 'go') { 'version' } else { '--version' }
        try { & $cmd.Source $versionArg *> $null; if ($LASTEXITCODE -eq 0) { return $cmd.Source } } catch { }
    }
    return $null
}

function Ensure-Command([string]$name, [string]$package) {
    if (Find-WorkingCommand $name) { return }
    if (-not (Get-Command winget -ErrorAction SilentlyContinue)) { throw "$name is missing and winget is unavailable. Install $package and rerun task init." }
    Write-Host "Installing $package for Adaetum setup..."
    & winget install --id $package --exact --accept-package-agreements --accept-source-agreements --silent
    if ($LASTEXITCODE -ne 0) { throw "Could not install $package (winget exit $LASTEXITCODE)." }
    Refresh-SetupPath
    if (-not (Find-WorkingCommand $name)) { throw "$package was installed, but $name is not available in this setup session." }
}

Refresh-SetupPath
$goDirective = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'go.mod') | Where-Object { $_ -match '^go\s+\S+' } | Select-Object -First 1
if ($goDirective -notmatch '^go\s+(\S+)') { throw 'Could not determine the required Go version.' }
$requiredGo = $Matches[1]
$downloadedGo = Join-Path $env:UserProfile "sdk\go$requiredGo\bin"
if (Test-Path -LiteralPath (Join-Path $downloadedGo 'go.exe')) { $env:Path = "$downloadedGo;$env:Path" }
Ensure-Command 'git' 'Git.Git'
$bashCandidates = @("$env:ProgramFiles\Git\bin\bash.exe", "$env:ProgramFiles\Git\usr\bin\bash.exe", "$env:LocalAppData\Programs\Git\bin\bash.exe", "$env:LocalAppData\Programs\Git\usr\bin\bash.exe")
$gitBash = @($bashCandidates | Where-Object { Test-Path -LiteralPath $_ }).Count -gt 0
if (-not $gitBash) {
    $gitBash = @((Get-Command bash.exe -All -CommandType Application -ErrorAction SilentlyContinue) | Where-Object { $_.Source -notlike '*\Windows\System32\bash.exe' }).Count -gt 0
}
if (-not $gitBash) {
    if (-not (Get-Command winget -ErrorAction SilentlyContinue)) { throw 'Git Bash is missing and winget is unavailable. Install Git for Windows and rerun task init.' }
    Write-Host 'Installing Git Bash for Adaetum setup...'
    & winget install --id Git.Git --exact --accept-package-agreements --accept-source-agreements --silent
    if ($LASTEXITCODE -ne 0) { throw "Could not install Git for Windows (winget exit $LASTEXITCODE)." }
    Refresh-SetupPath
}
Ensure-Command 'go' 'GoLang.Go'
$goExe = Find-WorkingCommand 'go'
$toolchainMode = & $goExe env GOTOOLCHAIN 2>$null
if ([string]::IsNullOrWhiteSpace(($toolchainMode -join ''))) {
    $setupBin = Join-Path $root '.adaetum\setup-bin'
    New-Item -ItemType Directory -Force -Path $setupBin | Out-Null
    Write-Host "Installing Go $requiredGo for Adaetum setup..."
    $oldGoBin = $env:GOBIN
    try {
        $env:GOBIN = $setupBin
        & $goExe install "golang.org/dl/go${requiredGo}@latest"
        if ($LASTEXITCODE -ne 0) { throw 'Could not install the Go toolchain downloader.' }
        & (Join-Path $setupBin "go${requiredGo}.exe") download
        if ($LASTEXITCODE -ne 0) { throw "Could not download Go $requiredGo." }
    } finally { $env:GOBIN = $oldGoBin }
}
Ensure-Command 'node' 'OpenJS.NodeJS.LTS'
Ensure-Command 'npm' 'OpenJS.NodeJS.LTS'
Ensure-Command 'python' 'Python.Python.3.13'

$venv = Join-Path $root '.adaetum\setup-python'
$venvPython = Join-Path $venv 'Scripts\python.exe'
$python = Find-WorkingCommand 'python'
& $python -c 'import yaml' *> $null
if ($LASTEXITCODE -ne 0) {
    if (-not (Test-Path -LiteralPath $venvPython)) {
        New-Item -ItemType Directory -Force -Path (Split-Path -Parent $venv) | Out-Null
        & $python -m venv $venv
        if ($LASTEXITCODE -ne 0) { throw 'Could not create the Adaetum Python environment.' }
    }
    & $venvPython -c 'import yaml' *> $null
    if ($LASTEXITCODE -ne 0) {
        Write-Host 'Installing PyYAML in the Adaetum setup environment...'
        & $venvPython -m pip install PyYAML
        if ($LASTEXITCODE -ne 0) { throw 'Could not install PyYAML in the Adaetum Python environment.' }
    }
}
