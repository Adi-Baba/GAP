$ErrorActionPreference = "Stop"

$Targets = @(
    @{ Zig="x86_64-windows-gnu"; GoOS="windows"; GoArch="amd64"; Ext=".exe"; Lib="gap.lib"; DLL="gap.dll" },
    @{ Zig="x86_64-linux-gnu";   GoOS="linux";   GoArch="amd64"; Ext="";     Lib="libgap.a"; DLL="libgap.so" },
    @{ Zig="aarch64-linux-gnu";  GoOS="linux";   GoArch="arm64"; Ext="";     Lib="libgap.a"; DLL="libgap.so" },
    @{ Zig="x86_64-macos";      GoOS="darwin";  GoArch="amd64"; Ext="";     Lib="libgap.a"; DLL="libgap.dylib" },
    @{ Zig="aarch64-macos";     GoOS="darwin";  GoArch="arm64"; Ext="";     Lib="libgap.a"; DLL="libgap.dylib" }
)

$RootDir = Get-Location
$CoreDir = Join-Path $RootDir "core"
$EngineDir = Join-Path $RootDir "engine"
$DistDir = Join-Path $RootDir "dist"

if (Test-Path $DistDir) { Remove-Item -Recurse -Force $DistDir }
New-Item -ItemType Directory -Path $DistDir | Out-Null

foreach ($T in $Targets) {
    $Platform = "$($T.GoOS)-$($T.GoArch)"
    Write-Host "--- Building for $Platform ($($T.Zig)) ---" -ForegroundColor Cyan
    
    $TargetDist = Join-Path $DistDir $Platform
    New-Item -ItemType Directory -Path $TargetDist -Force | Out-Null

    # 1. Build Zig Core
    Set-Location $CoreDir
    $ZigCmd = "zig build -Dtarget=$($T.Zig) -Doptimize=ReleaseFast"
    Write-Host "Running: $ZigCmd"
    Invoke-Expression $ZigCmd
    
    # 2. Prepare Engine Folder (Go needs the library in its search path)
    # We copy the static/dynamic lib to the engine dir for the build
    $LibSource = Join-Path $CoreDir "zig-out\lib\$($T.Lib)"
    $DLLSource = Join-Path $CoreDir "zig-out\bin\$($T.DLL)"
    if (-not (Test-Path $DLLSource)) {
        # Some platforms might put everything in lib
        $DLLSource = Join-Path $CoreDir "zig-out\lib\$($T.DLL)"
    }
    
    # Clean old libs in engine to prevent cross-contamination
    Get-ChildItem $EngineDir -Include "gap.lib", "libgap.a", "gap.dll", "libgap.so", "libgap.dylib" -Recurse | Remove-Item -Force

    Copy-Item $LibSource -Destination (Join-Path $EngineDir $T.Lib)
    if (Test-Path $DLLSource) {
        Copy-Item $DLLSource -Destination (Join-Path $EngineDir $T.DLL)
        Copy-Item $DLLSource -Destination (Join-Path $TargetDist $T.DLL)
    }

    # 3. Build Go CLI
    Set-Location $EngineDir
    
    # Set CGO environment variables for cross-compilation
    $env:GOOS = $T.GoOS
    $env:GOARCH = $T.GoArch
    $env:CGO_ENABLED = "1"
    
    # Use zig cc as the cross-compiler for Go's CGO bridge!
    $ZigTarget = $T.Zig
    $env:CC = "zig cc -target $ZigTarget"
    $env:CXX = "zig c++ -target $ZigTarget"

    $BinName = "gap$($T.Ext)"
    Write-Host "Building Go binary: $BinName"
    if ($T.GoOS -eq "darwin") {
        go build -ldflags="-s -w -linkmode=internal" -tags "netgo osusergo" -o (Join-Path $TargetDist $BinName)
    } else {
        go build -ldflags="-s -w" -o (Join-Path $TargetDist $BinName)
    }
    
    Write-Host "Successfully built $Platform" -ForegroundColor Green
    Write-Host ""
}

Write-Host "--- All builds completed in $DistDir ---" -ForegroundColor Yellow
Set-Location $RootDir
