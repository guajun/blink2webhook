param([Parameter(Mandatory = $true)][string]$Version)
$ErrorActionPreference = 'Stop'
if ($Version -notmatch '^v\d+\.\d+\.\d+$') { throw 'Version must be vX.Y.Z' }
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Push-Location $root
try {
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    $env:CGO_ENABLED = '0'
    New-Item -ItemType Directory -Force dist | Out-Null
    go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'Tests failed' }
    go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'Vet failed' }
    go build -trimpath -ldflags "-s -w -X main.version=$Version" -o dist/blink2webhook.exe .
    if ($LASTEXITCODE -ne 0) { throw 'Build failed' }
    Compress-Archive -LiteralPath dist/blink2webhook.exe -DestinationPath dist/blink2webhook_windows_amd64.zip -Force
    $hash = (Get-FileHash dist/blink2webhook_windows_amd64.zip -Algorithm SHA256).Hash.ToLowerInvariant()
    [IO.File]::WriteAllText((Join-Path $root 'dist\checksums.txt'), "$hash  blink2webhook_windows_amd64.zip`n", [Text.Encoding]::ASCII)
    Write-Host "Built $Version. Publish with gh release create and dist assets."
}
finally { Pop-Location }
