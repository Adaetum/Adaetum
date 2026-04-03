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
