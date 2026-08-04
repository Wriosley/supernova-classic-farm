[CmdletBinding()]
param(
    [ValidateSet("dev", "install", "test", "up", "migrate", "down")]
    [string]$Action = "dev"
)

$ErrorActionPreference = "Stop"
$Root = $PSScriptRoot
$ServerDirectory = Join-Path $Root "server"
$WebDirectory = Join-Path $Root "web"
$ComposeFile = Join-Path $Root "deploy\docker-compose.yml"
$EnvironmentFile = Join-Path $Root ".env"

function Require-Command([string]$Name) {
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "$Name was not found on PATH"
    }
}

function Invoke-Checked(
    [string]$Executable,
    [string[]]$CommandArguments,
    [string]$WorkingDirectory = $Root
) {
    Push-Location $WorkingDirectory
    try {
        & $Executable @CommandArguments
        if ($LASTEXITCODE -ne 0) {
            throw "$Executable exited with code $LASTEXITCODE"
        }
    }
    finally {
        Pop-Location
    }
}

function Get-ComposeArguments([string[]]$Tail) {
    $arguments = @("compose")
    if (Test-Path $EnvironmentFile) {
        $arguments += @("--env-file", $EnvironmentFile)
    }
    return $arguments + @("-f", $ComposeFile) + $Tail
}

function Import-DotEnv {
    if (-not (Test-Path $EnvironmentFile)) {
        return
    }
    foreach ($line in Get-Content $EnvironmentFile) {
        $trimmed = $line.Trim()
        if (-not $trimmed -or $trimmed.StartsWith("#")) {
            continue
        }
        $parts = $trimmed.Split("=", 2)
        if ($parts.Count -eq 2) {
            [Environment]::SetEnvironmentVariable($parts[0].Trim(), $parts[1].Trim(), "Process")
        }
    }
}

function Install-Dependencies {
    Require-Command "go"
    Require-Command "npm"
    Invoke-Checked "go" @("mod", "download") $ServerDirectory
    Invoke-Checked "npm" @("install") $WebDirectory
}

function Start-MySQL {
    Require-Command "docker"
    Invoke-Checked "docker" (Get-ComposeArguments @("up", "-d", "mysql"))

    for ($attempt = 0; $attempt -lt 60; $attempt++) {
        & docker @(Get-ComposeArguments @(
            "exec", "-T", "mysql", "sh", "-c",
            'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysqladmin ping -h 127.0.0.1 -u root --silent'
        )) *> $null
        if ($LASTEXITCODE -eq 0) {
            return
        }
        Start-Sleep -Seconds 1
    }
    throw "MySQL did not become healthy within 60 seconds"
}

Import-DotEnv

switch ($Action) {
    "install" {
        Install-Dependencies
    }
    "test" {
        Require-Command "go"
        Require-Command "npm"
        Invoke-Checked "go" @("test", "./...") $ServerDirectory
        if (Test-Path (Join-Path $WebDirectory "package-lock.json")) {
            Invoke-Checked "npm" @("ci") $WebDirectory
        }
        else {
            Invoke-Checked "npm" @("install") $WebDirectory
        }
        Invoke-Checked "npm" @("run", "build") $WebDirectory
    }
    "up" {
        Start-MySQL
    }
    "migrate" {
        Start-MySQL
        & (Join-Path $Root "deploy\migrate.ps1")
    }
    "down" {
        Require-Command "docker"
        Invoke-Checked "docker" (Get-ComposeArguments @("down"))
    }
    "dev" {
        Install-Dependencies
        Start-MySQL
        & (Join-Path $Root "deploy\migrate.ps1")

        $processes = @()
        try {
            foreach ($service in @("login", "gate", "zone", "coordinator")) {
                if (Test-Path (Join-Path $ServerDirectory "cmd\$service\main.go")) {
                    $processes += Start-Process -FilePath "go" `
                        -ArgumentList @("run", "./cmd/$service") `
                        -WorkingDirectory $ServerDirectory `
                        -NoNewWindow `
                        -PassThru
                }
            }

            $npmExecutable = (Get-Command npm).Source
            $webProcess = Start-Process -FilePath $npmExecutable `
                -ArgumentList @("run", "dev") `
                -WorkingDirectory $WebDirectory `
                -NoNewWindow `
                -PassThru
            $processes += $webProcess
            Wait-Process -Id $webProcess.Id
        }
        finally {
            $processes |
                Where-Object { $_ -and -not $_.HasExited } |
                Stop-Process
        }
    }
}
