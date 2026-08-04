[CmdletBinding()]
param(
    [ValidateRange(0, 86400)]
    [int]$RunSeconds = 0,

    [string]$MySQLDSN = $env:MYSQL_DSN
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$repoRoot = $PSScriptRoot
$serverRoot = Join-Path $repoRoot "server"
$runRoot = Join-Path $env:TEMP "classic-farm-servers-$PID"
$processes = [System.Collections.Generic.List[System.Diagnostics.Process]]::new()

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

function Wait-Ready {
    param(
        [string]$Name,
        [string]$Url,
        [System.Diagnostics.Process]$Process,
        [int]$TimeoutSeconds = 30
    )

    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    while ([DateTime]::UtcNow -lt $deadline) {
        if ($Process.HasExited) {
            throw "$Name exited during startup with code $($Process.ExitCode)"
        }
        try {
            $response = Invoke-WebRequest -Uri $Url -UseBasicParsing -TimeoutSec 2
            if ($response.StatusCode -eq 200) {
                Write-Host "[ready] $Name -> $Url" -ForegroundColor Green
                return
            }
        }
        catch {
            Start-Sleep -Milliseconds 200
        }
    }
    throw "$Name did not become ready within $TimeoutSeconds seconds"
}

function Start-FarmService {
    param(
        [string]$Name,
        [string]$Binary
    )

    $stdout = Join-Path $runRoot "$Name.stdout.log"
    $stderr = Join-Path $runRoot "$Name.stderr.log"
    $process = Start-Process -FilePath $Binary `
        -WorkingDirectory $serverRoot `
        -RedirectStandardOutput $stdout `
        -RedirectStandardError $stderr `
        -NoNewWindow `
        -PassThru
    $processes.Add($process)
    Write-Host "[start] $Name pid=$($process.Id)"
    return $process
}

function Show-Logs {
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

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw "Go was not found on PATH"
}

foreach ($port in @(8080, 8081, 8082, 8083)) {
    if (Test-PortOpen -Port $port) {
        throw "port $port is already in use; stop the existing process first"
    }
}

New-Item -ItemType Directory -Path $runRoot | Out-Null

try {
    $binaries = @{}
    foreach ($name in @("login", "zone", "coordinator", "gate")) {
        $binary = Join-Path $runRoot "$name.exe"
        Write-Host "[build] $name"
        Push-Location $serverRoot
        try {
            & go build -o $binary "./cmd/$name"
            if ($LASTEXITCODE -ne 0) {
                throw "go build failed for $name"
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
    $env:MYSQL_DSN = $MySQLDSN

    $login = Start-FarmService -Name "login" -Binary $binaries["login"]
    Wait-Ready -Name "LoginSvr" -Url "http://127.0.0.1:8080/readyz" -Process $login
    Wait-Ready -Name "Client config" -Url "http://127.0.0.1:8080/v1/client-config/1" -Process $login

    $zone = Start-FarmService -Name "zone" -Binary $binaries["zone"]
    Wait-Ready -Name "ZoneSvr" -Url "http://127.0.0.1:8082/readyz" -Process $zone

    $coordinator = Start-FarmService -Name "coordinator" -Binary $binaries["coordinator"]
    Wait-Ready -Name "Coordinator" -Url "http://127.0.0.1:8083/readyz" -Process $coordinator

    $gate = Start-FarmService -Name "gate" -Binary $binaries["gate"]
    Wait-Ready -Name "GateSvr" -Url "http://127.0.0.1:8081/readyz" -Process $gate

    Write-Host ""
    Write-Host "All backend services are running." -ForegroundColor Green
    Write-Host "LoginSvr:   http://127.0.0.1:8080"
    Write-Host "GateSvr:    ws://127.0.0.1:8081/ws"
    Write-Host "ZoneSvr:    http://127.0.0.1:8082"
    Write-Host "Coordinator:http://127.0.0.1:8083"
    Write-Host ""
    if ([string]::IsNullOrWhiteSpace($MySQLDSN)) {
        Write-Host "Data mode: development-only in-memory. Press Ctrl+C to stop all services."
    }
    else {
        Write-Host "Data mode: MySQL accounts, Sessions, and Player checkpoints. Press Ctrl+C to stop all services."
    }

    $stopAt = if ($RunSeconds -gt 0) {
        [DateTime]::UtcNow.AddSeconds($RunSeconds)
    }
    else {
        [DateTime]::MaxValue
    }
    while ([DateTime]::UtcNow -lt $stopAt) {
        foreach ($process in $processes) {
            if ($process.HasExited) {
                throw "a backend service exited unexpectedly with code $($process.ExitCode)"
            }
        }
        Start-Sleep -Seconds 1
    }
}
catch {
    Show-Logs
    throw
}
finally {
    for ($index = $processes.Count - 1; $index -ge 0; $index--) {
        $process = $processes[$index]
        if (-not $process.HasExited) {
            Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
            $process.WaitForExit(5000) | Out-Null
        }
    }
    Remove-Item $runRoot -Recurse -Force -ErrorAction SilentlyContinue
    Write-Host "All backend services stopped."
}
