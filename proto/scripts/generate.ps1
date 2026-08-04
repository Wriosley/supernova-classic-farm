$ErrorActionPreference = "Stop"

$repoRoot = Resolve-Path (Join-Path $PSScriptRoot "..\..")
Push-Location $repoRoot
try {
    $bufCommand = Get-Command buf -ErrorAction SilentlyContinue
    if ($bufCommand) {
        $buf = $bufCommand.Source
    }
    elseif (Get-Command go -ErrorAction SilentlyContinue) {
        $goPath = go env GOPATH
        $buf = Join-Path $goPath "bin\buf.exe"
    }

    if (-not $buf -or -not (Test-Path $buf)) {
        throw "buf is required. Install it with 'go install github.com/bufbuild/buf/cmd/buf@latest' and retry."
    }

    & $buf lint
    if ($LASTEXITCODE -ne 0) { throw "buf lint failed" }

    & $buf generate
    if ($LASTEXITCODE -ne 0) { throw "buf generate failed" }

    Write-Host "Generated Go types in server/gen and TypeScript types in web/src/gen."
}
finally {
    Pop-Location
}
