[CmdletBinding()]
param([string]$MySQLDSN = $env:MYSQL_DSN)
$ErrorActionPreference = 'Stop'
$taskRoot = $PSScriptRoot
# Only import supported keys. Never print credentials or the resulting DSN.
$taskEnvPath = Join-Path $taskRoot '.env'
$taskAllowed = @('MYSQL_DSN','MYSQL_HOST','MYSQL_PORT','MYSQL_DATABASE','MYSQL_USER','MYSQL_PASSWORD','GAME_ADDR')
if (Test-Path -LiteralPath $taskEnvPath) {
    foreach ($taskLine in Get-Content -LiteralPath $taskEnvPath -Encoding UTF8) {
        if ($taskLine.Trim().StartsWith('#') -or -not $taskLine.Contains('=')) { continue }
        $taskParts = $taskLine.Split('=',2)
        $taskKey = $taskParts[0].Trim()
        if ($taskAllowed -contains $taskKey -and -not [Environment]::GetEnvironmentVariable($taskKey,'Process')) {
            [Environment]::SetEnvironmentVariable($taskKey,$taskParts[1].Trim(),'Process')
        }
    }
}
if (-not $MySQLDSN) { $MySQLDSN = $env:MYSQL_DSN }
if (-not $MySQLDSN -and $env:MYSQL_PASSWORD) {
    $taskDbHost = if ($env:MYSQL_HOST) { $env:MYSQL_HOST } else { '127.0.0.1' }
    $taskDbPort = if ($env:MYSQL_PORT) { $env:MYSQL_PORT } else { '3306' }
    $taskDbName = if ($env:MYSQL_DATABASE) { $env:MYSQL_DATABASE } else { 'classicfarm' }
    $taskDbUser = if ($env:MYSQL_USER) { $env:MYSQL_USER } else { 'classicfarm' }
    $MySQLDSN = '{0}:{1}@tcp({2}:{3})/{4}?parseTime=true&charset=utf8mb4' -f $taskDbUser,$env:MYSQL_PASSWORD,$taskDbHost,$taskDbPort,$taskDbName
}
if (-not $MySQLDSN) { throw 'MySQL is required. Configure MYSQL_DSN or MYSQL_PASSWORD in .env.' }
$env:MYSQL_DSN = $MySQLDSN
if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw 'Go is not on PATH. Add the Go bin directory and restart the terminal.' }
if (-not $env:GOCACHE) { $env:GOCACHE = Join-Path $taskRoot '.cache\go-build' }
Push-Location (Join-Path $taskRoot 'server')
try {
    New-Item -ItemType Directory -Force -Path bin | Out-Null
    & go build -o bin/class-mid-game.exe ./cmd/game
    if ($LASTEXITCODE -ne 0) { throw 'Backend build failed' }
    Write-Host 'Starting one game server on port 8080. Ctrl+C stops it.'
    & ./bin/class-mid-game.exe
    if ($LASTEXITCODE -ne 0) { throw 'Game server stopped with an error' }
} finally { Pop-Location }
