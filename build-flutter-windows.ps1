$ErrorActionPreference = "Stop"

$ProjectRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$FlutterProject = Join-Path $ProjectRoot "cmd\exwg_flutter"

# The only place the release version is defined. Bump it for every
# substantive change: 1.0.0 -> 1.0.1 -> 1.0.2 ...
$Version = (Get-Content -Raw (Join-Path $ProjectRoot "VERSION")).Trim()
if ($Version -notmatch '^\d+\.\d+\.\d+$') {
    throw "VERSION must be plain MAJOR.MINOR.PATCH without suffixes, got '$Version'"
}
$BuildNumber = (git -C $ProjectRoot rev-list --count HEAD).Trim()
if ($LASTEXITCODE -ne 0 -or $BuildNumber -notmatch '^\d+$') {
    $BuildNumber = "0"
}

$CoreOutput = Join-Path $ProjectRoot "build\windows-amd64\ExWGCore.exe"
$ReleaseFolder = Join-Path $FlutterProject "build\windows\x64\runner\Release"
$DistFolder = Join-Path $ProjectRoot "dist"
$PackageRoot = Join-Path $ProjectRoot "build\package"
$PackageFolder = Join-Path $PackageRoot "Ex-WG-Windows-x64-$Version"
$Archive = Join-Path $DistFolder "Ex-WG-Windows-x64-$Version.zip"
$Installer = Join-Path $DistFolder "Ex-WG-Setup-$Version.exe"
$InstallerScript = Join-Path $ProjectRoot "packaging\exwg.iss"
$UserReadme = Join-Path $ProjectRoot "packaging\README.txt"

function Assert-NativeSuccess([string]$Step) {
    if ($LASTEXITCODE -ne 0) {
        throw "$Step failed with exit code $LASTEXITCODE"
    }
}

Set-Location $ProjectRoot
Write-Host "Ex WG $Version (build $BuildNumber)"

Write-Host "[1/6] Testing Go/AWG core"
go test ./...
Assert-NativeSuccess "Go tests"
go vet ./...
Assert-NativeSuccess "Go vet"

Write-Host "[2/6] Building ExWGCore.exe"
New-Item -ItemType Directory -Force (Split-Path -Parent $CoreOutput) | Out-Null
go build -trimpath -ldflags "-s -w -X nexusclient/internal/debuglog.Version=$Version" -o $CoreOutput .\cmd\exwg-core
Assert-NativeSuccess "ExWGCore build"

Write-Host "[3/6] Testing Flutter shell"
Set-Location $FlutterProject
flutter pub get
Assert-NativeSuccess "Flutter dependency resolution"
flutter analyze
Assert-NativeSuccess "Flutter analysis"
flutter test
Assert-NativeSuccess "Flutter tests"

Write-Host "[4/6] Building Flutter Windows release"
flutter build windows --release --build-name=$Version --build-number=$BuildNumber --dart-define=EXWG_VERSION=$Version
Assert-NativeSuccess "Flutter Windows release build"
if (-not (Test-Path (Join-Path $ReleaseFolder "ExWG.exe"))) {
    throw "Flutter release was not found: $ReleaseFolder"
}
# Flutter does not own the Go sidecar and may leave an older copy in its
# incremental Release directory. Always replace it with the core built above.
Copy-Item $CoreOutput (Join-Path $ReleaseFolder "ExWGCore.exe") -Force
Set-Location $ProjectRoot

Write-Host "[5/6] Packaging portable ZIP"
New-Item -ItemType Directory -Force $DistFolder | Out-Null
# Keep only the current package folder; older versions are build leftovers.
if (Test-Path $PackageRoot) {
    Remove-Item -Recurse -Force $PackageRoot
}
New-Item -ItemType Directory -Force $PackageFolder | Out-Null
Copy-Item (Join-Path $ReleaseFolder "*") $PackageFolder -Recurse
# Ship the short user guide, not the developer README.
(Get-Content -Raw -Encoding UTF8 $UserReadme).Replace("{{VERSION}}", $Version) |
    Set-Content -Encoding UTF8 (Join-Path $PackageFolder "README.txt")

if (Test-Path $Archive) {
    Remove-Item -Force $Archive
}
Compress-Archive -Path $PackageFolder -DestinationPath $Archive -CompressionLevel Optimal

$Hash = (Get-FileHash -Algorithm SHA256 $Archive).Hash.ToLowerInvariant()

Write-Host "[6/6] Building single-file installer"
$InnoRegistry = Get-ItemProperty -LiteralPath `
    "HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\Inno Setup 6_is1" `
    -ErrorAction SilentlyContinue
$InnoRegistryCompiler = if ($InnoRegistry.InstallLocation) {
    Join-Path $InnoRegistry.InstallLocation "ISCC.exe"
}
$DDriveInno = (Get-ChildItem -Path "D:\*\Inno Setup 6\ISCC.exe" -ErrorAction SilentlyContinue | Select-Object -ExpandProperty FullName -First 1)
$InnoCandidates = @(
    $env:INNO_SETUP_COMPILER,
    $InnoRegistryCompiler,
    $DDriveInno,
    (Join-Path ${env:ProgramFiles(x86)} "Inno Setup 6\ISCC.exe"),
    (Join-Path $env:ProgramFiles "Inno Setup 6\ISCC.exe")
) | Where-Object { $_ -and (Test-Path -LiteralPath $_) }
$InnoCompiler = $InnoCandidates | Select-Object -First 1
if (-not $InnoCompiler) {
    throw "Inno Setup 6 compiler was not found. Set INNO_SETUP_COMPILER to ISCC.exe."
}
if (Test-Path -LiteralPath $Installer) {
    Remove-Item -LiteralPath $Installer -Force
}
& $InnoCompiler "/DAppVersion=$Version" $InstallerScript
Assert-NativeSuccess "Inno Setup build"
if (-not (Test-Path -LiteralPath $Installer)) {
    throw "Installer was not found: $Installer"
}

$InstallerHash = (Get-FileHash -Algorithm SHA256 $Installer).Hash.ToLowerInvariant()
Write-Host "Portable: $Archive"
Write-Host "Portable SHA-256: $Hash"
Write-Host "Installer: $Installer"
Write-Host "Installer SHA-256: $InstallerHash"
