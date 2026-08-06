[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$serverRoot = Join-Path $repoRoot "server"
$runRoot = Join-Path $env:TEMP "classic-farm-dual-zone-$PID"
$processes = [System.Collections.Generic.List[System.Diagnostics.Process]]::new()
$names = [System.Collections.Generic.List[string]]::new()

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
    param([string]$Name, [string]$Url, [System.Diagnostics.Process]$Process)
    $deadline = [DateTime]::UtcNow.AddSeconds(30)
    while ([DateTime]::UtcNow -lt $deadline) {
        if ($Process.HasExited) {
            throw "$Name exited during startup with code $($Process.ExitCode)"
        }
        try {
            if ((Invoke-WebRequest -Uri $Url -UseBasicParsing -TimeoutSec 2).StatusCode -eq 200) {
                Write-Host "READY service=$Name url=$Url"
                return
            }
        }
        catch {
            Start-Sleep -Milliseconds 200
        }
    }
    throw "$Name did not become ready"
}

function Start-Service {
    param(
        [string]$Name,
        [string]$Binary,
        [hashtable]$Environment = @{}
    )
    $previous = @{}
    try {
        foreach ($key in $Environment.Keys) {
            $previous[$key] = [Environment]::GetEnvironmentVariable($key, "Process")
            [Environment]::SetEnvironmentVariable($key, [string]$Environment[$key], "Process")
        }
        $process = Start-Process -FilePath $Binary -WorkingDirectory $serverRoot `
            -RedirectStandardOutput (Join-Path $runRoot "$Name.stdout.log") `
            -RedirectStandardError (Join-Path $runRoot "$Name.stderr.log") `
            -NoNewWindow -PassThru
    }
    finally {
        foreach ($key in $Environment.Keys) {
            [Environment]::SetEnvironmentVariable($key, $previous[$key], "Process")
        }
    }
    $processes.Add($process)
    $names.Add($Name)
    Write-Host "START service=$Name pid=$($process.Id)"
    return $process
}

function Show-Logs {
    foreach ($name in $names) {
        foreach ($stream in @("stdout", "stderr")) {
            $path = Join-Path $runRoot "$name.$stream.log"
            if ((Test-Path $path) -and (Get-Item $path).Length -gt 0) {
                Write-Host "----- $name $stream -----"
                Get-Content $path
            }
        }
    }
}

$mysqlMode = -not [string]::IsNullOrWhiteSpace($env:MYSQL_DSN)
foreach ($port in @(8080, 8081, 8082, 8083, 8084)) {
    if (Test-PortOpen $port) {
        throw "required port $port is already in use"
    }
}
New-Item -ItemType Directory -Path $runRoot | Out-Null
$failure = $null
$environmentKeys = @(
    "APP_ENV", "H5_ORIGIN", "GATEWAY_ID", "GATEWAY_URL",
    "CLIENT_CONFIG_URL", "LOGIN_TICKET_CONSUME_URL", "COORDINATOR_URL",
    "GATE_RPC_URL", "INTERNAL_GRPC_HMAC_KEY", "ROUTING_MODE",
    "E2E_RUN", "E2E_DUAL_ZONE", "E2E_SUITE"
)
$previousEnvironment = @{}
foreach ($key in $environmentKeys) {
    $previousEnvironment[$key] = [Environment]::GetEnvironmentVariable($key, "Process")
}

try {
    $binaries = @{}
    foreach ($name in @("login", "zone", "coordinator", "gate")) {
        $binary = Join-Path $runRoot "$name.exe"
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
    $env:GATE_RPC_URL = "http://127.0.0.1:8081"
    $env:INTERNAL_GRPC_HMAC_KEY = "classic-farm-local-development-hmac-key-2026"
    $env:ROUTING_MODE = "static-dual-zone"
    $env:E2E_RUN = "1"
    $env:E2E_DUAL_ZONE = "1"
    $env:E2E_SUITE = if ($mysqlMode) { "dual-zone-mysql" } else { "dual-zone" }

    $coordinator = Start-Service "coordinator" $binaries["coordinator"] @{
        ZONE_A_ID = "zone-a"
        ZONE_A_ENDPOINT = "http://127.0.0.1:8082"
        ZONE_B_ID = "zone-b"
        ZONE_B_ENDPOINT = "http://127.0.0.1:8084"
        DUAL_ZONE_FENCE_BOOTSTRAP = if ($mysqlMode) { "1" } else { "" }
    }
    Wait-Ready "coordinator" "http://127.0.0.1:8083/readyz" $coordinator

    $login = Start-Service "login" $binaries["login"]
    Wait-Ready "login" "http://127.0.0.1:8080/readyz" $login

    $zoneA = Start-Service "zone-a" $binaries["zone"] @{
        OWNER_ZONE_ID = "zone-a"
        ZONE_HTTP_ADDRESS = "127.0.0.1:8082"
    }
    Wait-Ready "zone-a" "http://127.0.0.1:8082/readyz" $zoneA

    $zoneB = Start-Service "zone-b" $binaries["zone"] @{
        OWNER_ZONE_ID = "zone-b"
        ZONE_HTTP_ADDRESS = "127.0.0.1:8084"
    }
    Wait-Ready "zone-b" "http://127.0.0.1:8084/readyz" $zoneB

    $gate = Start-Service "gate" $binaries["gate"]
    Wait-Ready "gate" "http://127.0.0.1:8081/readyz" $gate

    Push-Location $serverRoot
    try {
        $testName = if ($mysqlMode) {
            "TestDualZoneMySQLRoutingAndPersistence"
        }
        else {
            "TestDualZoneRoutingAndCache"
        }
        & go test ./test/e2e -run $testName -count=1 -v
        if ($LASTEXITCODE -ne 0) {
            throw "dual-Zone E2E failed"
        }

        if ($mysqlMode) {
            Write-Host "RESTART service=coordinator reason=fence-hydrate-recovery"
            Stop-Process -Id $coordinator.Id -Force -ErrorAction SilentlyContinue
            $coordinator.WaitForExit(10000) | Out-Null
            for ($index = $processes.Count - 1; $index -ge 0; $index--) {
                if ($names[$index] -eq "coordinator") {
                    $processes.RemoveAt($index)
                    $names.RemoveAt($index)
                    break
                }
            }
            $coordinator = Start-Service "coordinator" $binaries["coordinator"] @{
                ZONE_A_ID = "zone-a"
                ZONE_A_ENDPOINT = "http://127.0.0.1:8082"
                ZONE_B_ID = "zone-b"
                ZONE_B_ENDPOINT = "http://127.0.0.1:8084"
                DUAL_ZONE_FENCE_BOOTSTRAP = "1"
            }
            Wait-Ready "coordinator" "http://127.0.0.1:8083/readyz" $coordinator
            $env:E2E_SUITE = "dual-zone-mysql-hydrate"
            & go test ./test/e2e -run TestDualZoneMySQLCoordinatorHydrateAfterMigration -count=1 -v
            if ($LASTEXITCODE -ne 0) {
                throw "dual-Zone MySQL hydrate E2E failed"
            }
        }
    }
    finally {
        Pop-Location
    }
    Write-Host "RESULT dual_zone_routing_e2e=PASS"
}
catch {
    $failure = $_
}
finally {
    for ($index = $processes.Count - 1; $index -ge 0; $index--) {
        $process = $processes[$index]
        if (-not $process.HasExited) {
            Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
            $process.WaitForExit(5000) | Out-Null
        }
    }
    Show-Logs
    Remove-Item $runRoot -Recurse -Force -ErrorAction SilentlyContinue
    foreach ($key in $environmentKeys) {
        [Environment]::SetEnvironmentVariable(
            $key, $previousEnvironment[$key], "Process"
        )
    }
}

if ($null -ne $failure) {
    Write-Error -ErrorRecord $failure
    exit 1
}
