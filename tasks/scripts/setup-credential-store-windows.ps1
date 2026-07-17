param(
  [Parameter(Mandatory = $true, Position = 0)]
  [ValidateSet("get", "set", "delete")]
  [string]$Action,
  [Parameter(Mandatory = $true, Position = 1)]
  [string]$Namespace,
  [Parameter(Mandatory = $true, Position = 2)]
  [string]$Key
)

$ErrorActionPreference = "Stop"
$identity = "io.adaetum.setup`n$Namespace`n$Key"
$sha256 = [System.Security.Cryptography.SHA256]::Create()
try {
  $digest = $sha256.ComputeHash([Text.Encoding]::UTF8.GetBytes($identity))
} finally {
  $sha256.Dispose()
}
$fileName = ([BitConverter]::ToString($digest)).Replace("-", "").ToLowerInvariant() + ".dpapi"
$storeDirectory = Join-Path $env:LOCALAPPDATA "Adaetum\SetupCredentials"
$storePath = Join-Path $storeDirectory $fileName

switch ($Action) {
  "set" {
    $secret = [Console]::In.ReadToEnd().TrimEnd("`r", "`n")
    if ([string]::IsNullOrWhiteSpace($secret)) {
      throw "A non-empty secret is required."
    }
    New-Item -ItemType Directory -Force -Path $storeDirectory | Out-Null
    $secure = ConvertTo-SecureString -String $secret -AsPlainText -Force
    $protected = ConvertFrom-SecureString -SecureString $secure
    [IO.File]::WriteAllText($storePath, $protected, [Text.UTF8Encoding]::new($false))
  }
  "get" {
    if (-not (Test-Path -LiteralPath $storePath)) {
      exit 1
    }
    $secure = ConvertTo-SecureString ([IO.File]::ReadAllText($storePath))
    $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
    try {
      [Console]::Out.Write([Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer))
    } finally {
      [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer)
    }
  }
  "delete" {
    Remove-Item -LiteralPath $storePath -Force -ErrorAction SilentlyContinue
  }
}
