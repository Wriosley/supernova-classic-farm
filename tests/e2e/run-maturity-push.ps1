[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"
$names = @(
    "MYSQL_DSN",
    "E2E_AUTH_MODE",
    "E2E_BUY_SEEDS",
    "E2E_PLANT",
    "E2E_APPLY_FERTILIZER",
    "E2E_WAIT_MATURITY_PUSH",
    "E2E_HARVEST",
    "E2E_EXPECT_PLAYER_SEQ"
)
$previous = @{}
foreach ($name in $names) {
    $previous[$name] = [Environment]::GetEnvironmentVariable($name, "Process")
}

try {
    Remove-Item Env:MYSQL_DSN -ErrorAction SilentlyContinue
    $env:E2E_AUTH_MODE = "register"
    $env:E2E_BUY_SEEDS = "1"
    $env:E2E_PLANT = "1"
    $env:E2E_APPLY_FERTILIZER = "1"
    $env:E2E_WAIT_MATURITY_PUSH = "1"
    $env:E2E_HARVEST = "1"
    $env:E2E_EXPECT_PLAYER_SEQ = "0"

    & (Join-Path $PSScriptRoot "run-authenticated-snapshot.ps1") `
        -Adapter "in-memory-maturity-push"
    if ($LASTEXITCODE -ne 0) {
        throw "maturity push E2E failed with exit code $LASTEXITCODE"
    }
    Write-Host "RESULT maturity_push_e2e=PASS"
}
finally {
    foreach ($name in $names) {
        $value = $previous[$name]
        if ($null -eq $value) {
            Remove-Item "Env:$name" -ErrorAction SilentlyContinue
        }
        else {
            [Environment]::SetEnvironmentVariable($name, $value, "Process")
        }
    }
}
