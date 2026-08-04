[CmdletBinding()]
param(
    [string]$HostName = "127.0.0.1",
    [ValidateRange(1, 65535)]
    [int]$Port = 3306,
    [string]$Database = "classicfarm",
    [string]$User = "classicfarm"
)

$ErrorActionPreference = "Stop"
$securePassword = Read-Host "MySQL password for $User" -AsSecureString
$passwordPointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($securePassword)
$previousDSN = $env:MYSQL_DSN

try {
    $plainPassword = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($passwordPointer)
    $escapedPassword = [Uri]::EscapeDataString($plainPassword)
    $env:MYSQL_DSN = "${User}:${escapedPassword}@tcp(${HostName}:${Port})/${Database}?charset=utf8mb4&parseTime=true&loc=Local"
    Remove-Variable plainPassword -ErrorAction SilentlyContinue

    & (Join-Path $PSScriptRoot "run-authenticated-snapshot.ps1") -Adapter "mysql-checkpoint"
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }
}
finally {
    [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($passwordPointer)
    Remove-Variable securePassword, escapedPassword -ErrorAction SilentlyContinue
    if ($null -eq $previousDSN) {
        Remove-Item Env:MYSQL_DSN -ErrorAction SilentlyContinue
    }
    else {
        $env:MYSQL_DSN = $previousDSN
    }
}
