# Shared helper for local MySQL DSN construction.
# Prefer existing MYSQL_DSN, then process env / repo-root .env fields, then Read-Host.
# Never pass the password on the command line.

function Get-RepoRootFromScript {
    return (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
}

function Read-DotEnvFile {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path
    )

    $values = @{}
    if (-not (Test-Path $Path -PathType Leaf)) {
        return $values
    }

    foreach ($line in Get-Content -Path $Path) {
        $trimmed = $line.Trim()
        if ([string]::IsNullOrWhiteSpace($trimmed) -or $trimmed.StartsWith("#")) {
            continue
        }
        $parts = $trimmed.Split("=", 2)
        if ($parts.Count -ne 2) {
            continue
        }
        $key = $parts[0].Trim()
        $value = $parts[1].Trim()
        if (
            ($value.StartsWith('"') -and $value.EndsWith('"')) -or
            ($value.StartsWith("'") -and $value.EndsWith("'"))
        ) {
            $value = $value.Substring(1, $value.Length - 2)
        }
        $values[$key] = $value
    }
    return $values
}

function Resolve-MySQLConnection {
    param(
        [string]$HostName = "127.0.0.1",
        [ValidateRange(1, 65535)]
        [int]$Port = 3306,
        [string]$Database = "classicfarm",
        [string]$User = "classicfarm",
        [switch]$AllowPrompt
    )

    $dotenv = Read-DotEnvFile -Path (Join-Path (Get-RepoRootFromScript) ".env")

    if (-not [string]::IsNullOrWhiteSpace($env:MYSQL_HOST)) {
        $HostName = $env:MYSQL_HOST.Trim()
    }
    elseif ($dotenv.ContainsKey("MYSQL_HOST") -and -not [string]::IsNullOrWhiteSpace($dotenv["MYSQL_HOST"])) {
        $HostName = $dotenv["MYSQL_HOST"]
    }

    if (-not [string]::IsNullOrWhiteSpace($env:MYSQL_PORT)) {
        $Port = [int]$env:MYSQL_PORT
    }
    elseif ($dotenv.ContainsKey("MYSQL_PORT") -and -not [string]::IsNullOrWhiteSpace($dotenv["MYSQL_PORT"])) {
        $Port = [int]$dotenv["MYSQL_PORT"]
    }

    if (-not [string]::IsNullOrWhiteSpace($env:MYSQL_DATABASE)) {
        $Database = $env:MYSQL_DATABASE.Trim()
    }
    elseif ($dotenv.ContainsKey("MYSQL_DATABASE") -and -not [string]::IsNullOrWhiteSpace($dotenv["MYSQL_DATABASE"])) {
        $Database = $dotenv["MYSQL_DATABASE"]
    }

    if (-not [string]::IsNullOrWhiteSpace($env:MYSQL_USER)) {
        $User = $env:MYSQL_USER.Trim()
    }
    elseif ($dotenv.ContainsKey("MYSQL_USER") -and -not [string]::IsNullOrWhiteSpace($dotenv["MYSQL_USER"])) {
        $User = $dotenv["MYSQL_USER"]
    }

    $plainPassword = $null
    if (-not [string]::IsNullOrWhiteSpace($env:MYSQL_PASSWORD)) {
        $plainPassword = $env:MYSQL_PASSWORD
    }
    elseif ($dotenv.ContainsKey("MYSQL_PASSWORD") -and -not [string]::IsNullOrWhiteSpace($dotenv["MYSQL_PASSWORD"])) {
        $candidate = $dotenv["MYSQL_PASSWORD"]
        if ($candidate -ne "请在本地填写") {
            $plainPassword = $candidate
        }
    }

    if (-not [string]::IsNullOrWhiteSpace($env:MYSQL_DSN)) {
        return [pscustomobject]@{
            Dsn           = $env:MYSQL_DSN
            PlainPassword = $plainPassword
            Source        = "MYSQL_DSN"
            HostName      = $HostName
            Port          = $Port
            Database      = $Database
            User          = $User
        }
    }

    if ([string]::IsNullOrWhiteSpace($plainPassword)) {
        if (-not $AllowPrompt) {
            throw "MYSQL_PASSWORD is not configured in the process environment or repo-root .env"
        }
        $securePassword = Read-Host "MySQL password for $User" -AsSecureString
        $passwordPointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($securePassword)
        try {
            $plainPassword = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($passwordPointer)
        }
        finally {
            [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($passwordPointer)
            Remove-Variable securePassword -ErrorAction SilentlyContinue
        }
    }

    $escapedPassword = [Uri]::EscapeDataString($plainPassword)
    $dsn = "${User}:${escapedPassword}@tcp(${HostName}:${Port})/${Database}?charset=utf8mb4&parseTime=true&loc=Local"
    return [pscustomobject]@{
        Dsn           = $dsn
        PlainPassword = $plainPassword
        Source        = "constructed"
        HostName      = $HostName
        Port          = $Port
        Database      = $Database
        User          = $User
    }
}

function Restore-EnvVar {
    param(
        [string]$Name,
        $PreviousValue
    )
    if ($null -eq $PreviousValue) {
        Remove-Item "Env:$Name" -ErrorAction SilentlyContinue
    }
    else {
        Set-Item -Path "Env:$Name" -Value $PreviousValue
    }
}
