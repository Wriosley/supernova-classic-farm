[CmdletBinding()]
param(
    [string]$LoginUrl = "http://127.0.0.1:8080",
    [string]$Adapter = $(if ([string]::IsNullOrWhiteSpace($env:MYSQL_DSN)) {
        "in-memory-development-only"
    } else {
        "mysql-checkpoint"
    })
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$serverRoot = Join-Path $repoRoot "server"
$runRoot = Join-Path $PSScriptRoot ".run"
$processes = [System.Collections.Generic.List[System.Diagnostics.Process]]::new()
$scriptFailure = $null

function Test-PortOpen {
    param([int]$Port)
    $client = [System.Net.Sockets.TcpClient]::new()
    try {
        $task = $client.ConnectAsync("127.0.0.1", $Port)
        return $task.Wait(250) -and $client.Connected
    }
    catch {
        return $false
    }
    finally {
        $client.Dispose()
    }
}

function Wait-HttpReady {
    param(
        [string]$Name,
        [string]$Url,
        [System.Diagnostics.Process]$Process,
        [int]$TimeoutSeconds = 30
    )
    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    while ([DateTime]::UtcNow -lt $deadline) {
        if ($Process.HasExited) {
            throw "$Name exited before readiness (exit code $($Process.ExitCode))"
        }
        try {
            $response = Invoke-WebRequest -Uri $Url -UseBasicParsing -TimeoutSec 2 -Headers @{
                Accept = "application/x-protobuf"
                Origin = "http://localhost:5173"
            }
            if ($response.StatusCode -eq 200) {
                Write-Host "READY service=$Name url=$Url status=200"
                return
            }
        }
        catch {
            Start-Sleep -Milliseconds 200
        }
    }
    throw "$Name did not become ready at $Url within $TimeoutSeconds seconds"
}

function Start-ServiceProcess {
    param(
        [string]$Name,
        [string]$Binary
    )
    $stdout = Join-Path $runRoot "$Name.stdout.log"
    $stderr = Join-Path $runRoot "$Name.stderr.log"
    $process = Start-Process -FilePath $Binary -WorkingDirectory $serverRoot `
        -RedirectStandardOutput $stdout -RedirectStandardError $stderr `
        -NoNewWindow -PassThru
    $processes.Add($process)
    Write-Host "START service=$Name pid=$($process.Id)"
    return $process
}

function Show-ServiceLogs {
    foreach ($name in @("login", "zone", "coordinator", "gate")) {
        foreach ($stream in @("stdout", "stderr")) {
            $path = Join-Path $runRoot "$name.$stream.log"
            if ((Test-Path $path) -and (Get-Item $path).Length -gt 0) {
                Write-Host "----- $name $stream -----"
                Get-Content $path
            }
        }
    }
}

if (Test-Path $runRoot) {
    Remove-Item $runRoot -Recurse -Force
}
New-Item -ItemType Directory -Path $runRoot | Out-Null

try {
    foreach ($port in @(8080, 8081, 8082, 8083)) {
        if (Test-PortOpen -Port $port) {
            throw "required port $port is already in use; refusing to test against unknown processes"
        }
    }

    $binaries = @{}
    foreach ($name in @("login", "zone", "coordinator", "gate")) {
        $binary = Join-Path $runRoot "$name.exe"
        Write-Host "BUILD service=$name"
        Push-Location $serverRoot
        try {
            & go build -o $binary "./cmd/$name"
            if ($LASTEXITCODE -ne 0) {
                throw "go build failed for $name with exit code $LASTEXITCODE"
            }
        }
        finally {
            Pop-Location
        }
        $binaries[$name] = $binary
    }

    $env:APP_ENV = "development"
    $env:H5_ORIGIN = "http://localhost:5173"
    $env:GATEWAY_ID = "local-gateway"
    $env:GATEWAY_URL = "ws://127.0.0.1:8081/ws"
    $env:CLIENT_CONFIG_URL = "http://127.0.0.1:8080/v1/client-config/1"
    $env:LOGIN_TICKET_CONSUME_URL = "http://127.0.0.1:8080/internal/v1/ws-tickets/consume"
    $env:COORDINATOR_URL = "http://127.0.0.1:8083"
    $env:E2E_RUN = "1"
    $env:E2E_LOGIN_URL = $LoginUrl

    $login = Start-ServiceProcess -Name "login" -Binary $binaries["login"]
    Wait-HttpReady -Name "login-health" -Url "$LoginUrl/readyz" -Process $login
    Wait-HttpReady -Name "login-config" -Url "$LoginUrl/v1/client-config/1" -Process $login

    $zone = Start-ServiceProcess -Name "zone" -Binary $binaries["zone"]
    Wait-HttpReady -Name "zone" -Url "http://127.0.0.1:8082/readyz" -Process $zone

    $coordinator = Start-ServiceProcess -Name "coordinator" -Binary $binaries["coordinator"]
    Wait-HttpReady -Name "coordinator" -Url "http://127.0.0.1:8083/readyz" -Process $coordinator

    $gate = Start-ServiceProcess -Name "gate" -Binary $binaries["gate"]
    Wait-HttpReady -Name "gate" -Url "http://127.0.0.1:8081/readyz" -Process $gate

    Write-Host "TEST command=go test ./test/e2e -count=1 -v"
    $testLog = Join-Path $runRoot "go-test.log"
    Push-Location $serverRoot
    try {
        & go test ./test/e2e -count=1 -v 2>&1 | Tee-Object -FilePath $testLog
        $testExitCode = $LASTEXITCODE
    }
    finally {
        Pop-Location
    }
    if ($testExitCode -ne 0) {
        throw "end-to-end test failed with exit code $testExitCode"
    }
    Write-Host "RESULT authenticated_snapshot_e2e=PASS adapter=$Adapter"
}
catch {
    $scriptFailure = $_
}
finally {
    for ($index = $processes.Count - 1; $index -ge 0; $index--) {
        $process = $processes[$index]
        if (-not $process.HasExited) {
            Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
            $process.WaitForExit(5000) | Out-Null
        }
        Write-Host "STOP pid=$($process.Id) exited=$($process.HasExited)"
    }
    Show-ServiceLogs
    Remove-Item $runRoot -Recurse -Force -ErrorAction SilentlyContinue
}

if ($null -ne $scriptFailure) {
    Write-Error -ErrorRecord $scriptFailure
    exit 1
}
