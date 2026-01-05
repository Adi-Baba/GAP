
$image = "d:\ISHNeuron\Images\burger.jpg"
$gapOut = "d:\ISHNeuron\GAP\encoded\burger.gap"
$pngOut = "d:\ISHNeuron\GAP\decoded\burger.png"
$engine = ".\engine.exe"

Write-Host "--- GAP Performance Benchmark ---"

# 1. GAP Encoding
Write-Host "Encoding GAP..."
$encTime = Measure-Command {
    & $engine encode -i $image -o $gapOut -s 0.1 -t 0.5 | Out-Null
}
$gapSize = (Get-Item $gapOut).Length / 1KB

# 2. GAP Decoding
Write-Host "Decoding GAP..."
$decTime = Measure-Command {
    & $engine decode -i $gapOut -o $pngOut | Out-Null
}

# 3. Reference Data
$origSize = (Get-Item $image).Length / 1KB
$pngSize = (Get-Item $pngOut).Length / 1KB

Write-Host "`nResults for burger.jpg (4912x7360 - 36MP):"
Write-Host "Original JPEG Size: $($origSize.ToString('F2')) KB"
Write-Host "GAP Compressed Size: $($gapSize.ToString('F2')) KB"
Write-Host "Decoded PNG Size: $($pngSize.ToString('F2')) KB"
Write-Host "Compression Ratio (vs PNG): $(($pngSize / $gapSize).ToString('F2'))x"
Write-Host "Encoding Time: $($encTime.TotalSeconds.ToString('F2'))s"
Write-Host "Decoding Time: $($decTime.TotalSeconds.ToString('F2'))s"
