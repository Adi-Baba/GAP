
$engine = "d:\ISHNeuron\GAP\dist\windows-amd64\gap.exe"
$encodedDir = "d:\ISHNeuron\GAP\encoded"
$decodedDir = "d:\ISHNeuron\GAP\decoded"

if (-not (Test-Path $encodedDir)) { Write-Error "Encoded dir not found"; exit }
if (-not (Test-Path $decodedDir)) { New-Item -ItemType Directory -Force -Path $decodedDir | Out-Null }

$files = Get-ChildItem -Path $encodedDir -Filter *.gap

Write-Host "--- GAP Benchmark Suite ---" -ForegroundColor Cyan
Write-Host "Engine: $engine"
Write-Host "Found $( $files.Count ) files.`n"

$results = @()

foreach ($file in $files) {
    $gapPath = $file.FullName
    $baseName = $file.BaseName
    $pngPath = Join-Path $decodedDir "$baseName.png"

    Write-Host "Benchmarking $baseName..." -NoNewline

    $gapSizeKB = $file.Length / 1KB

    # Measure Decode Time
    $time = Measure-Command {
        & $engine decode -i $gapPath -o $pngPath | Out-Null
    }
    
    if (Test-Path $pngPath) {
        $pngSizeKB = (Get-Item $pngPath).Length / 1KB
        $ratio = $pngSizeKB / $gapSizeKB
    } else {
        $pngSizeKB = 0
        $ratio = 0
    }

    $results += [PSCustomObject]@{
        Image = $baseName
        GapKB = $gapSizeKB
        PngKB = $pngSizeKB
        Ratio = $ratio
        TimeSec = $time.TotalSeconds
    }
    Write-Host " Done ($($time.TotalSeconds.ToString("F2"))s)" -ForegroundColor Green
}

Write-Host "`n--- Results Summary ---" -ForegroundColor Cyan
$results | Format-Table -Property Image, @{N="GAP (KB)";E={$_.GapKB.ToString("N0")}}, @{N="PNG (KB)";E={$_.PngKB.ToString("N0")}}, @{N="Ratio";E={$_.Ratio.ToString("F1") + "x"}}, @{N="Time (s)";E={$_.TimeSec.ToString("F3")}} -AutoSize

$avgTime = ($results | Measure-Object -Property TimeSec -Average).Average
$totalSize = ($results | Measure-Object -Property PngKB -Sum).Sum / 1024 # MB
$totalTime = ($results | Measure-Object -Property TimeSec -Sum).Sum

Write-Host "Average Decode Time: $($avgTime.ToString("F3")) s"
Write-Host "Total Throughput: $(($totalSize / $totalTime).ToString("F2")) MB/s (approx)"
