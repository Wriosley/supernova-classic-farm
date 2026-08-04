[CmdletBinding()]
param(
    [string]$Concurrency = "1,10,25,50,100",
    [ValidateRange(0, 3600)]
    [int]$WarmupSeconds = 10,
    [ValidateRange(1, 3600)]
    [int]$DurationSeconds = 60,
    [string]$RunID = (Get-Date).ToUniversalTime().ToString("yyyyMMdd_HHmmss")
)

$ErrorActionPreference = "Stop"
$repoRoot = Resolve-Path (Join-Path $PSScriptRoot "..\..")
$serverRoot = Join-Path $repoRoot "server"

foreach ($url in @(
    "http://127.0.0.1:8080/readyz",
    "http://127.0.0.1:8081/readyz",
    "http://127.0.0.1:8082/readyz",
    "http://127.0.0.1:8083/readyz",
    "http://127.0.0.1:8084/readyz"
)) {
    try {
        $response = Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 2
        if ($response.StatusCode -ne 200) {
            throw "HTTP $($response.StatusCode)"
        }
    }
    catch {
        throw "R3 requires the MySQL-backed dual-Zone stack. $url is not ready: $($_.Exception.Message)"
    }
}

Push-Location $serverRoot
try {
    & go run ./cmd/benchrunner `
        -scenario snapshot `
        -concurrency $Concurrency `
        -warmup "$($WarmupSeconds)s" `
        -duration "$($DurationSeconds)s" `
        -run-id $RunID
    if ($LASTEXITCODE -ne 0) {
        throw "benchrunner exited with code $LASTEXITCODE"
    }
}
finally {
    Pop-Location
}

Write-Host "Results: benchmark/results/$RunID" -ForegroundColor Green
