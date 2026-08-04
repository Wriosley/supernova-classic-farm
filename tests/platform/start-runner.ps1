[CmdletBinding()]
param(
    [string]$Addr = "127.0.0.1:7199",
    [switch]$BuildUI
)

$ErrorActionPreference = "Stop"
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "../..")).Path
$uiDir = Join-Path $repoRoot "tests\platform\web"
$distDir = Join-Path $uiDir "dist"

if ($BuildUI) {
    Push-Location $uiDir
    try {
        if (-not (Test-Path (Join-Path $uiDir "node_modules"))) {
            npm install
            if ($LASTEXITCODE -ne 0) {
                throw "npm install failed"
            }
        }
        npm run build
        if ($LASTEXITCODE -ne 0) {
            throw "platform UI build failed"
        }
    }
    finally {
        Pop-Location
    }
}

$uiArg = @()
if (Test-Path $distDir -PathType Container) {
    $uiArg = @("-ui", $distDir)
}

Push-Location (Join-Path $repoRoot "server")
try {
    go run ./cmd/testrunner -addr $Addr @uiArg
}
finally {
    Pop-Location
}
