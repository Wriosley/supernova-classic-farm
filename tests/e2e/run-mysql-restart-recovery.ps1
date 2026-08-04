[CmdletBinding()]
param(
    [string]$HostName = "127.0.0.1",
    [ValidateRange(1, 65535)]
    [int]$Port = 3306,
    [string]$Database = "classicfarm",
    [string]$User = "classicfarm",
    [string]$MySQLClient = ""
)

$ErrorActionPreference = "Stop"
$securePassword = Read-Host "MySQL password for $User" -AsSecureString
$passwordPointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($securePassword)
$previousDSN = $env:MYSQL_DSN
$previousAccount = $env:E2E_ACCOUNT_NAME
$previousMode = $env:E2E_AUTH_MODE
$previousBuySeeds = $env:E2E_BUY_SEEDS
$previousPlant = $env:E2E_PLANT
$previousApplyFertilizer = $env:E2E_APPLY_FERTILIZER
$previousWaitMaturityPush = $env:E2E_WAIT_MATURITY_PUSH
$previousHarvest = $env:E2E_HARVEST
$previousSellCrop = $env:E2E_SELL_CROP
$previousClaimChapterReward = $env:E2E_CLAIM_CHAPTER_REWARD
$previousCleanPlot = $env:E2E_CLEAN_PLOT
$previousExpectedPlayerSeq = $env:E2E_EXPECT_PLAYER_SEQ
$previousMySQLPassword = $env:MYSQL_PWD
$accountName = "restart_$([Guid]::NewGuid().ToString('N').Substring(0, 12))"

function Resolve-MySQLClient {
    if (-not [string]::IsNullOrWhiteSpace($MySQLClient)) {
        if (-not (Test-Path $MySQLClient -PathType Leaf)) {
            throw "MySQL client was not found at $MySQLClient"
        }
        return (Resolve-Path $MySQLClient).Path
    }

    $command = Get-Command mysql.exe -ErrorAction SilentlyContinue
    if ($null -ne $command) {
        return $command.Source
    }

    $service = Get-CimInstance Win32_Service -ErrorAction SilentlyContinue |
        Where-Object { $_.Name -like "MySQL*" } |
        Select-Object -First 1
    if ($null -ne $service) {
        $serverExecutable = $null
        if ($service.PathName -match '^\s*"([^"]+)"') {
            $serverExecutable = $Matches[1]
        }
        elseif ($service.PathName -match '^\s*(\S+)') {
            $serverExecutable = $Matches[1]
        }
        if (-not [string]::IsNullOrWhiteSpace($serverExecutable)) {
            $candidate = Join-Path (Split-Path -Parent $serverExecutable) "mysql.exe"
            if (Test-Path $candidate -PathType Leaf) {
                return $candidate
            }
        }
    }

    throw "mysql.exe was not found on PATH or beside an installed MySQL Windows service"
}

function Invoke-RestartPhase {
    param(
        [ValidateSet("register", "login")]
        [string]$Mode
    )

    $env:E2E_AUTH_MODE = $Mode
    if ($Mode -eq "register") {
        $env:E2E_BUY_SEEDS = "1"
        $env:E2E_PLANT = "1"
        $env:E2E_APPLY_FERTILIZER = "1"
        $env:E2E_WAIT_MATURITY_PUSH = "1"
        $env:E2E_HARVEST = "1"
        $env:E2E_SELL_CROP = "1"
        $env:E2E_CLAIM_CHAPTER_REWARD = "1"
        $env:E2E_CLEAN_PLOT = "1"
        $env:E2E_EXPECT_PLAYER_SEQ = "0"
    }
    else {
        Remove-Item Env:E2E_BUY_SEEDS -ErrorAction SilentlyContinue
        Remove-Item Env:E2E_PLANT -ErrorAction SilentlyContinue
        Remove-Item Env:E2E_APPLY_FERTILIZER -ErrorAction SilentlyContinue
        Remove-Item Env:E2E_WAIT_MATURITY_PUSH -ErrorAction SilentlyContinue
        Remove-Item Env:E2E_HARVEST -ErrorAction SilentlyContinue
        Remove-Item Env:E2E_SELL_CROP -ErrorAction SilentlyContinue
        Remove-Item Env:E2E_CLAIM_CHAPTER_REWARD -ErrorAction SilentlyContinue
        Remove-Item Env:E2E_CLEAN_PLOT -ErrorAction SilentlyContinue
        $env:E2E_EXPECT_PLAYER_SEQ = "8"
    }
    Write-Host "PHASE mode=$Mode account=$accountName"
    & (Join-Path $PSScriptRoot "run-authenticated-snapshot.ps1") `
        -Adapter "mysql-restart-$Mode"
    if ($LASTEXITCODE -ne 0) {
        throw "MySQL restart phase $Mode failed with exit code $LASTEXITCODE"
    }
}

try {
    $mysqlExecutable = Resolve-MySQLClient
    Write-Host "MYSQL_CLIENT path=$mysqlExecutable"
    $plainPassword = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($passwordPointer)
    $escapedPassword = [Uri]::EscapeDataString($plainPassword)
    $env:MYSQL_DSN = "${User}:${escapedPassword}@tcp(${HostName}:${Port})/${Database}?charset=utf8mb4&parseTime=true&loc=Local"
    $env:E2E_ACCOUNT_NAME = $accountName
    $env:MYSQL_PWD = $plainPassword
    foreach ($migration in (Get-ChildItem (Join-Path $PSScriptRoot "..\..\deploy\migrations") -Filter "*.up.sql" | Sort-Object Name)) {
        Write-Host "MIGRATE file=$($migration.Name)"
        Get-Content -Raw $migration.FullName | & $mysqlExecutable `
            --host=$HostName --port=$Port --user=$User --default-character-set=utf8mb4 $Database
        if ($LASTEXITCODE -ne 0) {
            throw "MySQL migration $($migration.Name) failed with exit code $LASTEXITCODE"
        }
    }
    if ($null -eq $previousMySQLPassword) {
        Remove-Item Env:MYSQL_PWD -ErrorAction SilentlyContinue
    }
    else {
        $env:MYSQL_PWD = $previousMySQLPassword
    }
    Remove-Variable plainPassword -ErrorAction SilentlyContinue

    Invoke-RestartPhase -Mode "register"
    Write-Host "RESTART boundary=fresh-four-process-stack"
    Invoke-RestartPhase -Mode "login"
    Write-Host "RESULT mysql_restart_recovery_e2e=PASS account=$accountName"
}
finally {
    [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($passwordPointer)
    Remove-Variable securePassword, escapedPassword -ErrorAction SilentlyContinue
    foreach ($entry in @(
        @{ Name = "MYSQL_DSN"; Value = $previousDSN },
        @{ Name = "E2E_ACCOUNT_NAME"; Value = $previousAccount },
        @{ Name = "E2E_AUTH_MODE"; Value = $previousMode },
        @{ Name = "E2E_BUY_SEEDS"; Value = $previousBuySeeds },
        @{ Name = "E2E_PLANT"; Value = $previousPlant },
        @{ Name = "E2E_APPLY_FERTILIZER"; Value = $previousApplyFertilizer },
        @{ Name = "E2E_WAIT_MATURITY_PUSH"; Value = $previousWaitMaturityPush },
        @{ Name = "E2E_HARVEST"; Value = $previousHarvest },
        @{ Name = "E2E_SELL_CROP"; Value = $previousSellCrop },
        @{ Name = "E2E_CLAIM_CHAPTER_REWARD"; Value = $previousClaimChapterReward },
        @{ Name = "E2E_CLEAN_PLOT"; Value = $previousCleanPlot },
        @{ Name = "E2E_EXPECT_PLAYER_SEQ"; Value = $previousExpectedPlayerSeq },
        @{ Name = "MYSQL_PWD"; Value = $previousMySQLPassword }
    )) {
        if ($null -eq $entry.Value) {
            Remove-Item "Env:$($entry.Name)" -ErrorAction SilentlyContinue
        }
        else {
            Set-Item "Env:$($entry.Name)" $entry.Value
        }
    }
}
