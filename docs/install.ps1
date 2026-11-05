param(
    [string]$Version = 'latest',
    [switch]$NoStart,
    [switch]$NoAutostart
)
$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
if (-not [Environment]::Is64BitOperatingSystem) { throw 'Flash Notify requires Windows amd64.' }
if ([Diagnostics.Process]::GetCurrentProcess().SessionId -eq 0) {
    throw 'Run this installer from your logged-in Windows desktop, not SSH / Session 0.'
}
$repo = 'guajun/flash-notify'
$installDir = Join-Path $env:LOCALAPPDATA 'FlashNotify\bin'
$exe = Join-Path $installDir 'flash-notify.exe'
$configFile = Join-Path $env:LOCALAPPDATA 'FlashNotify\config.json'
$hasConfig = Test-Path -LiteralPath $configFile
Write-Host 'Flash Notify - QQNT / WeChat -> Feishu / Lark' -ForegroundColor Cyan
Write-Host 'Webhook signing must be disabled. The URL stays on this computer.'
Write-Host 'Enter your Webhook URL (hidden). On upgrade, leave blank to keep it.'
$secure = Read-Host 'Webhook URL' -AsSecureString
$pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
try { $webhook = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer) }
finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer); $secure.Dispose() }
$webhook = $webhook.Trim()
if (-not $webhook -and -not $hasConfig) { throw 'Webhook URL is required on first installation.' }
if ($webhook -and $webhook -notmatch '^https://open\.(feishu\.cn|larksuite\.com)/open-apis/bot/v2/hook/[^/?#\s]+$') {
    throw 'Use an official Feishu or Lark HTTPS custom-bot Webhook URL.'
}
if ($Version -eq 'latest') {
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest" -Headers @{ 'User-Agent' = 'flash-notify-installer' }
    $Version = $release.tag_name
}
if ($Version -notmatch '^v\d+\.\d+\.\d+$') { throw 'Invalid release version.' }
$download = "https://github.com/$repo/releases/download/$Version"
$tempDir = Join-Path ([IO.Path]::GetTempPath()) ('flash-notify-install-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tempDir | Out-Null
try {
    Write-Host "Downloading $Version ..."
    $zip = Join-Path $tempDir 'flash-notify_windows_amd64.zip'
    $checksums = Join-Path $tempDir 'checksums.txt'
    Invoke-WebRequest -UseBasicParsing -Uri "$download/flash-notify_windows_amd64.zip" -OutFile $zip
    Invoke-WebRequest -UseBasicParsing -Uri "$download/checksums.txt" -OutFile $checksums
    $line = Get-Content -LiteralPath $checksums | Where-Object { $_ -match '^[0-9a-fA-F]{64}\s+\*?flash-notify_windows_amd64\.zip$' } | Select-Object -First 1
    if (-not $line) { throw 'Release checksum is missing.' }
    $expected = ($line -split '\s+')[0]
    if ((Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash -ne $expected) { throw 'Download checksum mismatch.' }
    Expand-Archive -LiteralPath $zip -DestinationPath $tempDir -Force
    if (-not (Test-Path -LiteralPath (Join-Path $tempDir 'flash-notify.exe'))) { throw 'Release executable is missing.' }
    New-Item -ItemType Directory -Path $installDir -Force | Out-Null
    if (Test-Path -LiteralPath $exe) {
        & $exe stop
        if ($LASTEXITCODE -ne 0) { throw 'Stop the existing daemon before upgrading.' }
    }
    Copy-Item -LiteralPath (Join-Path $tempDir 'flash-notify.exe') -Destination $exe -Force
    if ($webhook) {
        $webhook | & $exe config webhook --stdin
        if ($LASTEXITCODE -ne 0) { throw 'Could not save Webhook configuration.' }
    }
    $webhook = $null
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (($userPath -split ';') -notcontains $installDir) {
        $newPath = (([string]$userPath).TrimEnd(';') + ';' + $installDir).TrimStart(';')
        [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
    }
    if (($env:Path -split ';') -notcontains $installDir) { $env:Path += ';' + $installDir }
    if (-not $NoAutostart) {
        & $exe autostart on
        if ($LASTEXITCODE -ne 0) { throw 'Could not enable login startup.' }
    }
    if (-not $NoStart) {
        & $exe start
        if ($LASTEXITCODE -ne 0) { throw 'Daemon failed to start. Run flash-notify doctor.' }
    }
    Write-Host 'Installed. Commands: flash-notify test / doctor / stop / autostart off' -ForegroundColor Green
    Write-Host "Program: $exe"
    Write-Host 'A new terminal may be required for the PATH change.'
}
finally {
    $webhook = $null
    foreach ($name in @('flash-notify_windows_amd64.zip', 'checksums.txt', 'flash-notify.exe')) {
        $file = Join-Path $tempDir $name
        if (Test-Path -LiteralPath $file) { Remove-Item -LiteralPath $file -Force }
    }
    Remove-Item -LiteralPath $tempDir -Force -ErrorAction SilentlyContinue
}
