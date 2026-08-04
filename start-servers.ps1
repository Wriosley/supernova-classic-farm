[CmdletBinding()]
param(
    [ValidateRange(0, 86400)]
    [int]$RunSeconds = 0,

    [string]$MySQLDSN = $env:MYSQL_DSN,

    [switch]$DualZone
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$repoRoot = $PSScriptRoot
$serverRoot = Join-Path $repoRoot "server"
$runRoot = Join-Path $env:TEMP "classic-farm-servers-$PID"
$processes = [System.Collections.Generic.List[System.Diagnostics.Process]]::new()
$serviceNames = [System.Collections.Generic.List[string]]::new()

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
        [string]$Binary,
        [hashtable]$Environment = @{}
    )

    $stdout = Join-Path $runRoot "$Name.stdout.log"
    $stderr = Join-Path $runRoot "$Name.stderr.log"
    $previous = @{}
    try {
        foreach ($key in $Environment.Keys) {
            $previous[$key] = [Environment]::GetEnvironmentVariable($key, "Process")
            [Environment]::SetEnvironmentVariable($key, [string]$Environment[$key], "Process")
        }
        $process = Start-Process -FilePath $Binary `
            -WorkingDirectory $serverRoot `
            -RedirectStandardOutput $stdout `
            -RedirectStandardError $stderr `
            -NoNewWindow `
            -PassThru
    }
    finally {
        foreach ($key in $Environment.Keys) {
            [Environment]::SetEnvironmentVariable($key, $previous[$key], "Process")
        }
    }
    $processes.Add($process)
    $serviceNames.Add($Name)
    Write-Host "[start] $Name pid=$($process.Id)"
    return $process
}

function Show-Logs {
    foreach ($name in $serviceNames) {
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

$ports = @(8080, 8081, 8082, 8083)
if ($DualZone) {
    $ports += 8084
}
foreach ($port in $ports) {
    if (Test-PortOpen -Port $port) {
        throw "port $port is already in use; stop the existing process first"
    }
}

New-Item -ItemType Directory -Path $runRoot | Out-Null
$environmentKeys = @(
    "APP_ENV", "H5_ORIGIN", "GATEWAY_ID", "GATEWAY_URL",
    "CLIENT_CONFIG_URL", "LOGIN_TICKET_CONSUME_URL", "COORDINATOR_URL",
    "MYSQL_DSN", "ROUTING_MODE"
)
$previousEnvironment = @{}
foreach ($key in $environmentKeys) {
    $previousEnvironment[$key] = [Environment]::GetEnvironmentVariable($key, "Process")
}

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
    $env:ROUTING_MODE = if ($DualZone) { "static-dual-zone" } else { "local" }

    $coordinatorEnvironment = @{}
    if ($DualZone) {
        $coordinatorEnvironment = @{
            ZONE_A_ID = "zone-a"
            ZONE_A_ENDPOINT = "http://127.0.0.1:8082"
            ZONE_B_ID = "zone-b"
            ZONE_B_ENDPOINT = "http://127.0.0.1:8084"
            DUAL_ZONE_FENCE_BOOTSTRAP = if (
                [string]::IsNullOrWhiteSpace($MySQLDSN)
            ) { "" } else { "1" }
        }
    }
    $coordinator = Start-FarmService -Name "coordinator" `
        -Binary $binaries["coordinator"] -Environment $coordinatorEnvironment
    Wait-Ready -Name "Coordinator" -Url "http://127.0.0.1:8083/readyz" -Process $coordinator

    $login = Start-FarmService -Name "login" -Binary $binaries["login"]
    Wait-Ready -Name "LoginSvr" -Url "http://127.0.0.1:8080/readyz" -Process $login
    Wait-Ready -Name "Client config" -Url "http://127.0.0.1:8080/v1/client-config/1" -Process $login

    if ($DualZone) {
        $zoneA = Start-FarmService -Name "zone-a" -Binary $binaries["zone"] -Environment @{
            OWNER_ZONE_ID = "zone-a"
            ZONE_HTTP_ADDRESS = "127.0.0.1:8082"
        }
        Wait-Ready -Name "Zone A" -Url "http://127.0.0.1:8082/readyz" -Process $zoneA
        $zoneB = Start-FarmService -Name "zone-b" -Binary $binaries["zone"] -Environment @{
            OWNER_ZONE_ID = "zone-b"
            ZONE_HTTP_ADDRESS = "127.0.0.1:8084"
        }
        Wait-Ready -Name "Zone B" -Url "http://127.0.0.1:8084/readyz" -Process $zoneB
    }
    else {
        $zone = Start-FarmService -Name "zone" -Binary $binaries["zone"]
        Wait-Ready -Name "ZoneSvr" -Url "http://127.0.0.1:8082/readyz" -Process $zone
    }

    $gate = Start-FarmService -Name "gate" -Binary $binaries["gate"]
    Wait-Ready -Name "GateSvr" -Url "http://127.0.0.1:8081/readyz" -Process $gate

    Write-Host ""
    Write-Host "All backend services are running." -ForegroundColor Green
    Write-Host "LoginSvr:   http://127.0.0.1:8080"
    Write-Host "GateSvr:    ws://127.0.0.1:8081/ws"
    if ($DualZone) {
        Write-Host "Zone A:     http://127.0.0.1:8082"
        Write-Host "Zone B:     http://127.0.0.1:8084"
    }
    else {
        Write-Host "ZoneSvr:    http://127.0.0.1:8082"
    }
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
    foreach ($key in $environmentKeys) {
        [Environment]::SetEnvironmentVariable(
            $key, $previousEnvironment[$key], "Process"
        )
    }
    Write-Host "All backend services stopped."
}
