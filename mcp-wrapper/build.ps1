param(
    [string]$Version = "0.6.0"
)

$ErrorActionPreference = "Stop"
$ProjectRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$GoCommand = Get-Command go -ErrorAction SilentlyContinue
if ($null -eq $GoCommand) {
    throw "Go was not found in PATH. Install Go 1.25 or newer: https://go.dev/dl/"
}
$GoExe = $GoCommand.Source
$OutputDir = Join-Path $ProjectRoot "dist"
New-Item -ItemType Directory -Path $OutputDir -Force | Out-Null
Push-Location $ProjectRoot

try {
    & $GoExe test ./...
    if ($LASTEXITCODE -ne 0) {
        throw "go test failed with exit code $LASTEXITCODE"
    }

    & $GoExe build -buildvcs=false -trimpath -ldflags "-s -w -X main.version=$Version" -o (Join-Path $OutputDir "mcp-1c-analog.exe") ./cmd/mcp-1c-analog
    if ($LASTEXITCODE -ne 0) {
        throw "go build failed with exit code $LASTEXITCODE"
    }

    Copy-Item -LiteralPath (Join-Path $OutputDir "mcp-1c-analog.exe") -Destination (Join-Path $OutputDir "mcp-1c-analog-$Version.exe") -Force
}
finally {
    Pop-Location
}

& (Join-Path $OutputDir "mcp-1c-analog.exe") --version
