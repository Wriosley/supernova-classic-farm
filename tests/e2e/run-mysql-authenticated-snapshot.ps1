[CmdletBinding()]
param(
    [string]$HostName = "127.0.0.1",
    [ValidateRange(1, 65535)]
    [int]$Port = 3306,
    [string]$Database = "classicfarm",
    [string]$User = "classicfarm"
)

$ErrorActionPreference = "Stop"
. (Join-Path $PSScriptRoot "_mysql-env.ps1")

$previousDSN = $env:MYSQL_DSN
$connection = $null

try {
    $connection = Resolve-MySQLConnection -HostName $HostName -Port $Port -Database $Database -User $User -AllowPrompt
    $env:MYSQL_DSN = $connection.Dsn

    & (Join-Path $PSScriptRoot "run-authenticated-snapshot.ps1") -Adapter "mysql-checkpoint"
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }
}
finally {
    if ($null -ne $connection -and $null -ne $connection.PlainPassword) {
        Remove-Variable -Name connection -Force -ErrorAction SilentlyContinue
    }
    Restore-EnvVar -Name "MYSQL_DSN" -PreviousValue $previousDSN
}
