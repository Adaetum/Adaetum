param(
  [Parameter(Mandatory = $true, Position = 0)]
  [string]$ScriptPath,
  [Parameter(ValueFromRemainingArguments = $true)]
  [string[]]$ScriptArgs
)

$ErrorActionPreference = "Stop"

# Refresh PATH from registry for this process (helps after winget installs).
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
$machinePath = [Environment]::GetEnvironmentVariable("Path", "Machine")
$env:Path = "$machinePath;$userPath;$env:Path"

# Installs from an earlier Task command run in a separate process. Add their
# standard locations again so this Git Bash session and Wails inherit them now.
$setupRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..\..')).Path
$toolPaths = @(
  (Join-Path $env:ProgramFiles 'Git\cmd'), (Join-Path $env:ProgramFiles 'Git\bin'), (Join-Path $env:ProgramFiles 'Git\usr\bin'),
  (Join-Path $env:LocalAppData 'Programs\Git\usr\bin'),
  (Join-Path $env:ProgramFiles 'Go\bin'), (Join-Path $env:ProgramFiles 'nodejs'),
  (Join-Path $env:ProgramFiles 'GitHub CLI'), (Join-Path $env:ProgramFiles '7-Zip'),
  (Join-Path $env:LocalAppData 'Microsoft\WinGet\Links'),
  (Join-Path $env:UserProfile '.local\bin'), (Join-Path $env:UserProfile 'go\bin')
)
$venvScripts = Join-Path $setupRoot '.adaetum\setup-python\Scripts'
$goMod = Join-Path $setupRoot 'installer\go.mod'
if (Test-Path -LiteralPath $goMod) {
  $goDirective = Get-Content -LiteralPath $goMod | Where-Object { $_ -match '^go\s+\S+' } | Select-Object -First 1
  if ($goDirective -match '^go\s+(\S+)') { $toolPaths = @((Join-Path $env:UserProfile "sdk\go$($Matches[1])\bin")) + $toolPaths }
}
$venvPython = Join-Path $venvScripts 'python.exe'
if (Test-Path -LiteralPath $venvPython) {
  & $venvPython -c 'import yaml' *> $null
  if ($LASTEXITCODE -eq 0) { $toolPaths = @($venvScripts) + $toolPaths }
}
$wingetPackages = Join-Path $env:LocalAppData 'Microsoft\WinGet\Packages'
if (Test-Path -LiteralPath $wingetPackages) {
  $rclone = Get-ChildItem -LiteralPath $wingetPackages -Filter 'rclone.exe' -File -Recurse -ErrorAction SilentlyContinue | Select-Object -First 1
  if ($rclone) { $toolPaths += $rclone.DirectoryName }
}
$pythonRoot = Join-Path $env:LocalAppData 'Programs\Python'
if (Test-Path -LiteralPath $pythonRoot) {
  Get-ChildItem -LiteralPath $pythonRoot -Directory -Filter 'Python3*' | Sort-Object Name -Descending | ForEach-Object { $toolPaths += $_.FullName; $toolPaths += (Join-Path $_.FullName 'Scripts') }
}
$existingToolPaths = $toolPaths | Where-Object { Test-Path -LiteralPath $_ }
$env:Path = (@($existingToolPaths) + @($env:Path)) -join ';'
# The bundled xorriso loads the MSYS libraries from the same Git installation,
# including when Git lives outside its usual Program Files location.
$git = Get-Command git -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
if ($git) {
  $gitExecPath = & $git.Source --exec-path
  if ($LASTEXITCODE -eq 0 -and $gitExecPath) {
    $gitRuntime = Join-Path $gitExecPath '..\..\..\usr\bin'
    if (Test-Path -LiteralPath (Join-Path $gitRuntime 'msys-2.0.dll')) { $env:Path = "$gitRuntime;$env:Path" }
  }
}

function Convert-ToBashPath {
  param(
    [Parameter(Mandatory = $true)]
    [string]$PathValue
  )
  $full = (Resolve-Path -LiteralPath $PathValue).Path
  if ($full -match "^[A-Za-z]:\\") {
    $drive = $full.Substring(0, 1).ToLowerInvariant()
    $rest = $full.Substring(2).Replace("\", "/")
    return "/$drive$rest"
  }
  return $full.Replace("\", "/")
}

function Resolve-GitBash {
  $candidates = @(
    (Join-Path $env:ProgramFiles "Git\bin\bash.exe"),
    (Join-Path $env:ProgramFiles "Git\usr\bin\bash.exe"),
    (Join-Path $env:LocalAppData "Programs\Git\bin\bash.exe"),
    (Join-Path $env:LocalAppData "Programs\Git\usr\bin\bash.exe")
  )
  foreach ($candidate in $candidates) {
    if ($candidate -and (Test-Path -LiteralPath $candidate)) {
      return $candidate
    }
  }

  $cmd = Get-Command bash -ErrorAction SilentlyContinue
  if ($cmd -and $cmd.Source -and ($cmd.Source -notlike "*\Windows\System32\bash.exe")) {
    return $cmd.Source
  }

  throw "Git Bash not found. Install Git for Windows or add Git Bash to PATH."
}

$bashExe = Resolve-GitBash
$bashScript = Convert-ToBashPath -PathValue $ScriptPath

# Git-Bash + task.exe on Windows can mis-handle temporary scripts when TMPDIR is
# unset, resolving temp files under a non-existent repo-local "tmp" directory.
if (-not $env:TMPDIR -or [string]::IsNullOrWhiteSpace($env:TMPDIR)) {
  $env:TMPDIR = "/tmp"
}

# Ensure tools discovered by PowerShell are visible to Git Bash.
foreach ($tool in @("task", "uv", "rclone")) {
  $cmd = Get-Command $tool -ErrorAction SilentlyContinue
  if ($cmd -and $cmd.Source) {
    $toolDir = Split-Path -Parent $cmd.Source
    if ($toolDir -and ($env:Path -notlike "*$toolDir*")) {
      $env:Path = "$toolDir;$env:Path"
    }
  }
}

& $bashExe $bashScript @ScriptArgs
exit $LASTEXITCODE
