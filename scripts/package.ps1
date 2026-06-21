param(
    [string]$Version = "dev",
    [string]$MingwBin = "C:\msys64\mingw64\bin",
    [switch]$SkipDownload
)

$ErrorActionPreference = "Stop"

$Root = Split-Path -Parent $PSScriptRoot
$BuildDir = Join-Path $Root "build\release"
$VendorDir = Join-Path $Root "build\vendor"
$DistDir = Join-Path $Root "dist"
$PortableName = "wintp-$Version-windows-amd64-portable"
$PortableDir = Join-Path $BuildDir $PortableName
$PortableZip = Join-Path $DistDir "$PortableName.zip"
$RcloneZip = Join-Path $VendorDir "rclone-current-windows-amd64.zip"
$WinFspMsi = Join-Path $VendorDir "winfsp-2.1.25156.msi"

$RcloneUrl = "https://downloads.rclone.org/rclone-current-windows-amd64.zip"
$WinFspUrl = "https://github.com/winfsp/winfsp/releases/download/v2.1/winfsp-2.1.25156.msi"

function New-CleanDir {
    param([string]$Path)
    if (Test-Path $Path) {
        Remove-Item -LiteralPath $Path -Recurse -Force
    }
    New-Item -ItemType Directory -Force -Path $Path | Out-Null
}

function Download-IfMissing {
    param(
        [string]$Url,
        [string]$OutFile
    )
    if (Test-Path $OutFile) {
        Write-Host "Using cached $(Split-Path -Leaf $OutFile)"
        return
    }
    Write-Host "Downloading $Url"
    Invoke-WebRequest -Uri $Url -OutFile $OutFile
}

function Find-RcloneExe {
    param([string]$ZipPath)
    $ExtractDir = Join-Path $VendorDir "rclone"
    New-CleanDir $ExtractDir
    Expand-Archive -LiteralPath $ZipPath -DestinationPath $ExtractDir -Force
    $Exe = Get-ChildItem -Path $ExtractDir -Recurse -Filter "rclone.exe" | Select-Object -First 1
    if (-not $Exe) {
        throw "rclone.exe was not found in $ZipPath"
    }
    return $Exe.FullName
}

function Find-InnoSetup {
    $Cmd = Get-Command "iscc.exe" -ErrorAction SilentlyContinue
    if ($Cmd) {
        return $Cmd.Source
    }

    $Candidates = @(
        "${env:ProgramFiles(x86)}\Inno Setup 6\ISCC.exe",
        "$env:ProgramFiles\Inno Setup 6\ISCC.exe",
        "$env:LOCALAPPDATA\Programs\Inno Setup 6\ISCC.exe"
    )
    foreach ($Candidate in $Candidates) {
        if ($Candidate -and (Test-Path $Candidate)) {
            return $Candidate
        }
    }
    return $null
}

if (-not (Test-Path (Join-Path $MingwBin "gcc.exe"))) {
    throw "gcc.exe was not found in $MingwBin. Install MSYS2 mingw64 or pass -MingwBin."
}

New-Item -ItemType Directory -Force -Path $VendorDir, $DistDir | Out-Null
New-CleanDir $BuildDir

if (-not $SkipDownload) {
    Download-IfMissing -Url $RcloneUrl -OutFile $RcloneZip
    Download-IfMissing -Url $WinFspUrl -OutFile $WinFspMsi
}

if (-not (Test-Path $RcloneZip)) {
    throw "Missing $RcloneZip. Re-run without -SkipDownload or place the file there."
}
if (-not (Test-Path $WinFspMsi)) {
    throw "Missing $WinFspMsi. Re-run without -SkipDownload or place the file there."
}

$RcloneExe = Find-RcloneExe -ZipPath $RcloneZip

Write-Host "Building wintp.exe"
$env:Path = "$MingwBin;$env:Path"
$env:CGO_ENABLED = "1"
$env:GOOS = "windows"
$env:GOARCH = "amd64"
$env:CC = Join-Path $MingwBin "gcc.exe"
$env:CXX = Join-Path $MingwBin "g++.exe"

Push-Location $Root
try {
    go build -trimpath -ldflags "-s -w -H=windowsgui -X main.version=$Version" -o (Join-Path $BuildDir "wintp.exe") .
}
finally {
    Pop-Location
}

New-CleanDir $PortableDir
Copy-Item -LiteralPath (Join-Path $BuildDir "wintp.exe") -Destination (Join-Path $PortableDir "wintp.exe")
Copy-Item -LiteralPath $RcloneExe -Destination (Join-Path $PortableDir "rclone.exe")
Copy-Item -LiteralPath $WinFspMsi -Destination (Join-Path $PortableDir "winfsp-2.1.25156.msi")

@"
wintp portable release

Run wintp.exe.

If mounts do not work, install WinFsp first by running:
  winfsp-2.1.25156.msi

rclone.exe is bundled next to wintp.exe.
"@ | Set-Content -Path (Join-Path $PortableDir "README.txt") -Encoding ascii

if (Test-Path $PortableZip) {
    Remove-Item -LiteralPath $PortableZip -Force
}
Compress-Archive -Path (Join-Path $PortableDir "*") -DestinationPath $PortableZip -Force
Write-Host "Portable release: $PortableZip"

$Iscc = Find-InnoSetup
if ($Iscc) {
    Write-Host "Building installer with $Iscc"
    & $Iscc `
        "/DMyAppVersion=$Version" `
        "/DSourceDir=$PortableDir" `
        "/DOutputDir=$DistDir" `
        (Join-Path $Root "packaging\wintp.iss")
    Write-Host "Installer output: $DistDir"
}
else {
    Write-Warning "Inno Setup compiler was not found. Portable zip was created, but installer was skipped."
    Write-Warning "Install Inno Setup 6, then rerun this script to produce wintp-$Version-setup.exe."
}
