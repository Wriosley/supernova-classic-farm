[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"
$RepositoryRoot = Split-Path -Parent $PSScriptRoot
$ComposeFile = Join-Path $PSScriptRoot "docker-compose.yml"
$MigrationDirectory = Join-Path $PSScriptRoot "migrations"
$EnvironmentFile = Join-Path $RepositoryRoot ".env"

$composeArguments = @("compose")
if (Test-Path $EnvironmentFile) {
    $composeArguments += @("--env-file", $EnvironmentFile)
}
$composeArguments += @("-f", $ComposeFile)

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    throw "docker was not found on PATH"
}

$migrationFiles = Get-ChildItem $MigrationDirectory -Filter "*.up.sql" | Sort-Object Name
foreach ($migration in $migrationFiles) {
    Write-Host "Applying $($migration.Name)"
    Get-Content -Raw $migration.FullName |
        & docker @composeArguments exec -T mysql sh -c 'MYSQL_PWD="$MYSQL_PASSWORD" exec mysql -u"$MYSQL_USER" "$MYSQL_DATABASE"'
    if ($LASTEXITCODE -ne 0) {
        throw "Migration $($migration.Name) failed"
    }
}

Write-Host "Migrations completed."
