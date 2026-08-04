$ErrorActionPreference = "Stop"

$repoRoot = Resolve-Path (Join-Path $PSScriptRoot "..\..")

& (Join-Path $PSScriptRoot "generate.ps1")

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw "Go is required for the generated Go round-trip smoke test."
}
if (-not (Test-Path (Join-Path $repoRoot "server\go.mod"))) {
    throw "server/go.mod is not present yet; generate succeeded, but the Go smoke test cannot run."
}

Push-Location (Join-Path $repoRoot "server")
try {
    go test ./gen/smoke
    if ($LASTEXITCODE -ne 0) { throw "Go Protobuf round-trip smoke test failed" }
}
finally {
    Pop-Location
}

$tsx = Join-Path $repoRoot "web\node_modules\.bin\tsx.cmd"
if (-not (Test-Path $tsx)) {
    throw "web/node_modules/.bin/tsx.cmd is not present; install the web dependencies before the TypeScript smoke test."
}

Push-Location (Join-Path $repoRoot "web")
try {
    & $tsx "src/gen/smoke/roundtrip.ts"
    if ($LASTEXITCODE -ne 0) { throw "TypeScript Protobuf round-trip smoke test failed" }
}
finally {
    Pop-Location
}
