param(
    [string]$Version = 'latest',
    [ValidateSet('feishu', 'json')][string]$Output = 'feishu',
    [switch]$NoStart,
    [switch]$NoAutostart
)
$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
if (-not [Environment]::Is64BitOperatingSystem) { throw 'blink2webhook requires Windows amd64.' }
$fromSSH = [Diagnostics.Process]::GetCurrentProcess().SessionId -eq 0
$repo = 'guajun/blink2webhook'
$installDir = Join-Path $env:LOCALAPPDATA 'Blink2Webhook\bin'
$exe = Join-Path $installDir 'blink2webhook.exe'
$configFile = Join-Path $env:LOCALAPPDATA 'Blink2Webhook\config.json'
# Migrate the former installation before launching the renamed daemon.
$legacyDir = Join-Path $env:LOCALAPPDATA 'FlashNotify'
$legacyExe = Join-Path $legacyDir 'bin\flash-notify.exe'
if (-not (Test-Path -LiteralPath $configFile) -and (Test-Path -LiteralPath (Join-Path $legacyDir 'config.json'))) {
    New-Item -ItemType Directory -Path (Split-Path -Parent $configFile) -Force | Out-Null
    Copy-Item -LiteralPath (Join-Path $legacyDir 'config.json') -Destination $configFile
}
$hasConfig = Test-Path -LiteralPath $configFile
Write-Host 'blink2webhook - QQNT / WeChat -> Feishu / Lark' -ForegroundColor Cyan
Write-Host 'Webhook signing must be disabled. The URL stays on this computer.'
Write-Host 'Enter your Webhook URL (hidden). On upgrade, leave blank to keep it.'
$secure = Read-Host 'Webhook URL' -AsSecureString
$pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
try { $webhook = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer) }
finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer); $secure.Dispose() }
$webhook = $webhook.Trim()
if (-not $webhook -and -not $hasConfig) { throw 'Webhook URL is required on first installation.' }
if ($Output -eq 'feishu' -and $webhook -and $webhook -notmatch '^https://open\.(feishu\.cn|larksuite\.com)/open-apis/bot/v2/hook/[^/?#\s]+$') {
    throw 'Use an official Feishu or Lark HTTPS custom-bot Webhook URL.'
}
if ($webhook) {
        if ($Output -eq 'json') {
            $uri = $null
            if (-not [Uri]::TryCreate($webhook, [UriKind]::Absolute, [ref]$uri) -or $uri.Scheme -notin @('http','https') -or -not $uri.Host -or $uri.UserInfo -or $uri.Fragment) { throw 'Invalid HTTP(S) Webhook URL.' }
        }
}
if ($Version -eq 'latest') {
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest" -Headers @{ 'User-Agent' = 'blink2webhook-installer' }
    $Version = $release.tag_name
}
if ($Version -notmatch '^v\d+\.\d+\.\d+$') { throw 'Invalid release version.' }
$download = "https://github.com/$repo/releases/download/$Version"
$tempDir = Join-Path ([IO.Path]::GetTempPath()) ('blink2webhook-install-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tempDir | Out-Null
try {
    Write-Host "Downloading $Version ..."
    $zip = Join-Path $tempDir 'blink2webhook_windows_amd64.zip'
    $checksums = Join-Path $tempDir 'checksums.txt'
    Invoke-WebRequest -UseBasicParsing -Uri "$download/blink2webhook_windows_amd64.zip" -OutFile $zip
    Invoke-WebRequest -UseBasicParsing -Uri "$download/checksums.txt" -OutFile $checksums
    $line = Get-Content -LiteralPath $checksums | Where-Object { $_ -match '^[0-9a-fA-F]{64}\s+\*?blink2webhook_windows_amd64\.zip$' } | Select-Object -First 1
    if (-not $line) { throw 'Release checksum is missing.' }
    $expected = ($line -split '\s+')[0]
    if ((Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash -ne $expected) { throw 'Download checksum mismatch.' }
    Expand-Archive -LiteralPath $zip -DestinationPath $tempDir -Force
    if (-not (Test-Path -LiteralPath (Join-Path $tempDir 'blink2webhook.exe'))) { throw 'Release executable is missing.' }
    if (Test-Path -LiteralPath $legacyExe) {
        Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -eq $legacyExe } | ForEach-Object {
            Stop-Process -Id $_.ProcessId -ErrorAction Stop
        }
    }
    $runKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'
    if (Get-ItemProperty -LiteralPath $runKey -Name FlashNotify -ErrorAction SilentlyContinue) {
        Remove-ItemProperty -LiteralPath $runKey -Name FlashNotify -ErrorAction Stop
    }
    New-Item -ItemType Directory -Path $installDir -Force | Out-Null
    if (Test-Path -LiteralPath $exe) {
        & $exe stop
        if ($LASTEXITCODE -ne 0) { throw 'Stop the existing daemon before upgrading.' }
        if ($fromSSH) {
            # Older versions cannot see another session's hidden daemon window.
            Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -eq $exe } | ForEach-Object {
                Stop-Process -Id $_.ProcessId -ErrorAction Stop
            }
            Start-Sleep -Milliseconds 300
        }
    }
    $stateFile = Join-Path $env:LOCALAPPDATA 'Blink2Webhook\state.json'
    if (Test-Path -LiteralPath $stateFile) { Remove-Item -LiteralPath $stateFile -Force }
    Copy-Item -LiteralPath (Join-Path $tempDir 'blink2webhook.exe') -Destination $exe -Force
    if ($webhook) {
        # Preserve existing settings while updating the selected output and URL.
        $config = if (Test-Path -LiteralPath $configFile) { Get-Content -Raw -LiteralPath $configFile | ConvertFrom-Json } else { [pscustomobject]@{ device = $env:COMPUTERNAME; apps = @('QQ.exe','Weixin.exe','WeChat.exe'); quiet_seconds = 10 } }
        $config | Add-Member -NotePropertyName output -NotePropertyValue $Output -Force
        $config | Add-Member -NotePropertyName webhook -NotePropertyValue $webhook -Force
        [IO.File]::WriteAllText($configFile, ($config | ConvertTo-Json), (New-Object Text.UTF8Encoding($false)))
        $webhook | & $exe config webhook --stdin
        if ($LASTEXITCODE -ne 0) { throw 'Could not save Webhook configuration.' }
    }
    $webhook = $null
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $userPath = (($userPath -split ';') | Where-Object { $_.TrimEnd('\') -ine (Join-Path $legacyDir 'bin') }) -join ';'
    [Environment]::SetEnvironmentVariable('Path', $userPath, 'User')
    if (($userPath -split ';') -notcontains $installDir) {
        $newPath = (([string]$userPath).TrimEnd(';') + ';' + $installDir).TrimStart(';')
        [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
    }
    if (($env:Path -split ';') -notcontains $installDir) { $env:Path += ';' + $installDir }
    if (-not $NoAutostart) {
        & $exe autostart on
        if ($LASTEXITCODE -ne 0) { throw 'Could not enable login startup.' }
    }
    if (-not $NoStart -and -not $fromSSH) {
        & $exe start
        if ($LASTEXITCODE -ne 0) {
            throw 'Daemon failed to start. Run blink2webhook doctor.'
        }
    }
    Write-Host 'Installed. Commands: blink2webhook test / doctor / stop / autostart off' -ForegroundColor Green
    Write-Host "Program: $exe"
    if ($fromSSH -and -not $NoStart) {
        Write-Host 'SSH installation is complete. Monitoring is not running in Session 0.' -ForegroundColor Yellow
        Write-Host 'Run blink2webhook start once from the logged-in Windows desktop, or sign out and sign in for login startup.'
    }
    Write-Host 'A new terminal may be required for the PATH change.'
}
finally {
    $webhook = $null
    foreach ($name in @('blink2webhook_windows_amd64.zip', 'checksums.txt', 'blink2webhook.exe')) {
        $file = Join-Path $tempDir $name
        if (Test-Path -LiteralPath $file) { Remove-Item -LiteralPath $file -Force }
    }
    Remove-Item -LiteralPath $tempDir -Force -ErrorAction SilentlyContinue
}
