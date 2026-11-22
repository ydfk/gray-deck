[CmdletBinding()]
param(
    [ValidateSet('Install', 'Update')]
    [string]$Mode = 'Install',

    [string]$BinaryPath = (Join-Path $env:LOCALAPPDATA 'Graydeck\graydeck-windows-amd64.exe'),

    [string]$WorkingDirectory,

    [switch]$NoStart
)

$ErrorActionPreference = 'Stop'
$assetUrl = 'https://github.com/ydfk/gray-deck/releases/latest/download/graydeck-windows-amd64.exe'

function Assert-GraydeckBinary {
    param([string]$Path)

    $bytes = [IO.File]::ReadAllBytes($Path)
    $marker = 'path' + [char]9 + 'mihomo-manager/cmd/managerd'
    if ($bytes.Length -lt 2 -or $bytes[0] -ne 77 -or $bytes[1] -ne 90 -or
        -not [Text.Encoding]::ASCII.GetString($bytes).Contains($marker)) {
        throw "目标不是有效的 Graydeck Windows 可执行文件：$Path"
    }
}

function Find-GraydeckProcess {
    param([string]$Path)

    $name = [IO.Path]::GetFileNameWithoutExtension($Path)
    Get-Process -Name ([WildcardPattern]::Escape($name)) -ErrorAction SilentlyContinue | Where-Object {
        [string]::Equals($_.Path, $Path, [StringComparison]::OrdinalIgnoreCase)
    }
}

function Stop-GraydeckProcess {
    param([Diagnostics.Process[]]$Processes)

    foreach ($process in $Processes) {
        if ($process.HasExited) {
            continue
        }
        # Graydeck 启动的 Mihomo 子进程也要退出，避免占用原有代理端口。
        & (Join-Path $env:SystemRoot 'System32\taskkill.exe') /PID $process.Id /T /F | Out-Null
        if (-not $process.WaitForExit(5000)) {
            throw "无法停止 Graydeck 进程 $($process.Id)。"
        }
    }
}

function Start-GraydeckProcess {
    param([string]$Path, [string]$Directory)

    $process = Start-Process -FilePath $Path -WorkingDirectory $Directory -WindowStyle Hidden -PassThru
    try {
        if ($process.WaitForExit(2000)) {
            throw "Graydeck 启动后退出，退出码为 $($process.ExitCode)。"
        }
    } finally {
        $process.Dispose()
    }
}

function Get-AppConfigPath {
    param([string]$Directory)

    if ($env:GRAYDECK_APP_CONFIG) {
        if ([IO.Path]::IsPathRooted($env:GRAYDECK_APP_CONFIG)) {
            return $env:GRAYDECK_APP_CONFIG
        }
        return Join-Path $Directory $env:GRAYDECK_APP_CONFIG
    }
    $rootConfig = Join-Path ([IO.Path]::GetPathRoot($Directory)) 'config'
    if (Test-Path -LiteralPath $rootConfig -PathType Container) {
        return Join-Path $rootConfig 'graydeck.yaml'
    }
    return Join-Path $Directory 'config\graydeck.yaml'
}

function New-AppConfig {
    param([string]$Path)

    if (Test-Path -LiteralPath $Path) {
        return
    }
    $null = [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($Path))
    $bytes = New-Object byte[] 16
    $generator = [Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $generator.GetBytes($bytes)
    } finally {
        $generator.Dispose()
    }
    $password = [BitConverter]::ToString($bytes).Replace('-', '').ToLowerInvariant()
    $content = @"
server:
  port: 8080
auth:
  enabled: true
  username: admin
  password: "$password"
"@
    $stream = [IO.File]::Open($Path, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write)
    try {
        $bytes = [Text.Encoding]::UTF8.GetBytes($content + [Environment]::NewLine)
        $stream.Write($bytes, 0, $bytes.Length)
    } finally {
        $stream.Dispose()
    }
}

if ($env:OS -ne 'Windows_NT' -or ($env:PROCESSOR_ARCHITECTURE -ne 'AMD64' -and $env:PROCESSOR_ARCHITEW6432 -ne 'AMD64')) {
    throw '当前发布文件仅支持 Windows x64。'
}

$BinaryPath = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($BinaryPath)
$binaryDirectory = [IO.Path]::GetDirectoryName($BinaryPath)
if (-not $WorkingDirectory) {
    $WorkingDirectory = $binaryDirectory
}
$WorkingDirectory = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($WorkingDirectory)
if ([IO.Path]::GetExtension($BinaryPath) -ine '.exe') {
    throw '请传入 Graydeck 的 .exe 文件路径。'
}
if ($Mode -eq 'Update' -and -not (Test-Path -LiteralPath $BinaryPath -PathType Leaf)) {
    throw "找不到现有 Graydeck 文件：$BinaryPath"
}

$null = [IO.Directory]::CreateDirectory($binaryDirectory)
$lock = [IO.File]::Open("$BinaryPath.update.lock", [IO.FileMode]::OpenOrCreate, [IO.FileAccess]::Write, [IO.FileShare]::None)
$staged = Join-Path $binaryDirectory ('.graydeck-download-' + [Guid]::NewGuid().ToString('N'))
$backup = "$BinaryPath.previous"
try {
    if ($Mode -eq 'Install' -and (Test-Path -LiteralPath $BinaryPath)) {
        Assert-GraydeckBinary $BinaryPath
        Write-Host 'Graydeck 已安装，使用 -Mode Update 可升级。'
        return
    }
    if ($Mode -eq 'Update') {
        Assert-GraydeckBinary $BinaryPath
        if ((Get-Item -LiteralPath $BinaryPath).Attributes -band [IO.FileAttributes]::ReparsePoint) {
            throw '请传入实际的 Graydeck 文件路径。'
        }
        if (-not (Test-Path -LiteralPath $WorkingDirectory -PathType Container)) {
            throw "找不到原工作目录：$WorkingDirectory"
        }
    }

    Invoke-WebRequest -Uri $assetUrl -OutFile $staged -UseBasicParsing -TimeoutSec 120
    Assert-GraydeckBinary $staged
    if ($Mode -eq 'Install') {
        $null = [IO.Directory]::CreateDirectory($WorkingDirectory)
        $configPath = Get-AppConfigPath $WorkingDirectory
        New-AppConfig $configPath
        [IO.File]::Move($staged, $BinaryPath)
        if (-not $NoStart) {
            Start-GraydeckProcess $BinaryPath $WorkingDirectory
        }
        Write-Host "Graydeck 已安装到 $BinaryPath。登录设置见 $configPath。"
        return
    }

    if ((Get-FileHash -LiteralPath $BinaryPath).Hash -eq (Get-FileHash -LiteralPath $staged).Hash) {
        Write-Host '当前可执行文件已是最新发布版本。'
        return
    }
    $running = @(Find-GraydeckProcess $BinaryPath)
    $replaced = $false
    try {
        Stop-GraydeckProcess $running
        [IO.File]::Replace($staged, $BinaryPath, $backup, $false)
        $replaced = $true
        if ($running.Count -gt 0 -and -not $NoStart) {
            Start-GraydeckProcess $BinaryPath $WorkingDirectory
        }
    } catch {
        $failure = $_
        if ($replaced) {
            [IO.File]::Replace($backup, $BinaryPath, [NullString]::Value, $false)
        }
        if ($running.Count -gt 0 -and -not (Find-GraydeckProcess $BinaryPath)) {
            Start-GraydeckProcess $BinaryPath $WorkingDirectory
        }
        throw $failure
    }
    Write-Host "Graydeck 已更新。上一版本保存在 $backup。"
    if ($running.Count -eq 0 -or $NoStart) {
        Write-Host "可从原工作目录启动 $BinaryPath。"
    }
} finally {
    $lock.Dispose()
    if (Test-Path -LiteralPath $staged) {
        Remove-Item -LiteralPath $staged -Force
    }
}
