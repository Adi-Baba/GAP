package main

import (
    "bufio"
    "compress/gzip"
    "encoding/binary"
    "fmt"
    "image"
    "image/color"
    "image/png"
    "io"
    "math"
    "os"
    "runtime"
    "sync"
    "time"
)

var coeffPool = sync.Pool{
	New: func() any {
		return make([]float32, 128)
	},
}

func DecodeImage(inputPath, outputPath string) error {
    // 1. Open Input
    file, err := os.Open(inputPath)
    if err != nil {
        return fmt.Errorf("failed to open input: %v", err)
    }
    defer file.Close()

    // 2. Read Header
    var header GapHeader
    if err := binary.Read(file, binary.LittleEndian, &header); err != nil {
        return fmt.Errorf("failed to read header: %v", err)
    }

    if string(header.Magic[:]) != "GAP\x01" {
        return fmt.Errorf("invalid magic bytes")
    }

    width := int(header.Width)
    height := int(header.Height)
    channels := int(header.Channels)
    if channels == 0 { channels = 1 }

    fmt.Printf("Decoding %s (%dx%d, %d ch) -> %s\n", inputPath, width, height, channels, outputPath)
    
    // 3. Decode Planes
    planes := make([]*image.Gray, channels)
    
    // Check Flags
    const FlagGzip = 1
    const FlagSubsampled = 4
    const FlagRangeCoded = 8
    
    isGzip := (header.Flags & FlagGzip) != 0
    isSubsampled := (header.Flags & FlagSubsampled) != 0
    isRangeCoded := (header.Flags & FlagRangeCoded) != 0
    
    coreStart := time.Now()
    if isRangeCoded {
        fmt.Println("Detected Range Coding (Split 5-Stream).")
        
        // 1. Pre-read all compressed blocks sequentially for all planes
        type streamBlock struct {
            uLen uint32
            cData []byte
        }
        type planeData struct {
            blocks [5]streamBlock
        }
        allPlaneData := make([]planeData, channels)
        
        for i := 0; i < channels; i++ {
            for s := 0; s < 5; s++ {
                var uLen, cLen uint32
                if err := binary.Read(file, binary.LittleEndian, &uLen); err != nil { return err }
                if err := binary.Read(file, binary.LittleEndian, &cLen); err != nil { return err }
                cData := make([]byte, cLen)
                if _, err := io.ReadFull(file, cData); err != nil { return err }
                allPlaneData[i].blocks[s] = streamBlock{uLen, cData}
            }
        }
        
        // 2. Decode all planes in parallel
        var pwg sync.WaitGroup
        for i := 0; i < channels; i++ {
            pwg.Add(1)
            go func(pIdx int) {
                defer pwg.Done()
                
                pWidth, pHeight := width, height
                if isSubsampled && (pIdx == 1 || pIdx == 2) {
                    pWidth /= 2
                    pHeight /= 2
                }
                initVal := uint8(0)
                if pIdx > 0 { initVal = 128 }
                
                // Decompress 5 streams in parallel
                streams := make([][]byte, 5)
                var dwg sync.WaitGroup
                for s := 0; s < 5; s++ {
                    dwg.Add(1)
                    go func(sIdx int) {
                        defer dwg.Done()
                        block := allPlaneData[pIdx].blocks[sIdx]
                        if block.uLen > 0 {
                            streams[sIdx] = GapDecompressData(block.cData, int(block.uLen))
                        } else {
                            streams[sIdx] = []byte{}
                        }
                    }(s)
                }
                dwg.Wait()
                
                plane, err := gapDecodePlaneSplit(streams[0], streams[1], streams[2], streams[3], streams[4], pWidth, pHeight, header.Flags, initVal, header.S)
                if err == nil {
                    planes[pIdx] = plane
                }
            }(i)
        }
        pwg.Wait()
    } else {
        // Legacy: Gzip or Raw Stream (Keep sequential for now as it's a single stream)
        var reader io.Reader
        if isGzip {
            fmt.Println("Detected Gzip Compression.")
            gr, err := gzip.NewReader(file)
            if err != nil { return fmt.Errorf("failed to create gzip reader: %v", err) }
            defer gr.Close()
            reader = bufio.NewReaderSize(gr, 1024*1024)
        } else {
            reader = bufio.NewReaderSize(file, 1024*1024)
        }
        
        for i := 0; i < channels; i++ {
            pWidth, pHeight := width, height
            if isSubsampled && (i == 1 || i == 2) {
                pWidth /= 2
                pHeight /= 2
            }
            initVal := uint8(0)
            if i > 0 { initVal = 128 }
            plane, err := gapDecodePlaneOptimized(reader, pWidth, pHeight, header.Flags, initVal, header.S)
            if err != nil { return fmt.Errorf("failed to decode plane %d: %v", i, err) }
            planes[i] = plane
        }
    }
    
    // 3. Upsample Chroma in parallel if needed
    if isSubsampled && channels == 3 {
        var uwg sync.WaitGroup
        uwg.Add(2)
        go func() { defer uwg.Done(); planes[1] = upsamplePlane(planes[1], width, height) }()
        go func() { defer uwg.Done(); planes[2] = upsamplePlane(planes[2], width, height) }()
        uwg.Wait()
    }

    // 4. Merge YCbCr -> RGB IN PARALLEL
    finalImg := image.NewRGBA(image.Rect(0, 0, width, height))
    
    if channels == 3 {
        yPlane := planes[0]
        cbPlane := planes[1]
        crPlane := planes[2]
        
        // Parallel conversion - split by rows
        numWorkers := runtime.NumCPU()
        rowsPerWorker := (height + numWorkers - 1) / numWorkers
        
        var wg sync.WaitGroup
        for w := 0; w < numWorkers; w++ {
            startY := w * rowsPerWorker
            endY := startY + rowsPerWorker
            if endY > height { endY = height }
            if startY >= height { continue }
            
            wg.Add(1)
            go func(sy, ey int) {
                defer wg.Done()
                for y := sy; y < ey; y++ {
                    for x := 0; x < width; x++ {
                        yy := yPlane.GrayAt(x, y).Y
                        cb := cbPlane.GrayAt(x, y).Y
                        cr := crPlane.GrayAt(x, y).Y
                        r, g, b := color.YCbCrToRGB(yy, cb, cr)
                        
                        // Direct pixel access (4x faster than Set)
                        idx := finalImg.PixOffset(x, y)
                        finalImg.Pix[idx] = r
                        finalImg.Pix[idx+1] = g
                        finalImg.Pix[idx+2] = b
                        finalImg.Pix[idx+3] = 255
                    }
                }
            }(startY, endY)
        }
        wg.Wait()
    } else {
        // Grayscale
        src := planes[0]
        for y := 0; y < height; y++ {
            for x := 0; x < width; x++ {
                gray := src.GrayAt(x, y).Y
                idx := finalImg.PixOffset(x, y)
                finalImg.Pix[idx] = gray
                finalImg.Pix[idx+1] = gray
                finalImg.Pix[idx+2] = gray
                finalImg.Pix[idx+3] = 255
            }
        }
    }
    
    // 5. Apply Parallel Deblocking
    DeblockImageParallel(finalImg)
    
    // 6. Apply Edge-Only Antialiasing for whiskers/fine-lines
    applyEdgeAntialiasing(finalImg)
    
    // 7. Apply Line Continuity Filter for block-boundary whisker artifacts
    applyLineContinuityFilter(finalImg)
    
    fmt.Printf("Core Reconstruction (Zig + Go Parallel): %v\n", time.Since(coreStart))
    
    // 6. Write Output with buffered writer
    pngStart := time.Now()
    outFile, err := os.Create(outputPath)
    if err != nil {
        return fmt.Errorf("failed to create output: %v", err)
    }
    defer outFile.Close()
    
    bufWriter := bufio.NewWriterSize(outFile, 1024*1024)
    encoder := png.Encoder{CompressionLevel: png.BestSpeed}
    if err := encoder.Encode(bufWriter, finalImg); err != nil {
        return fmt.Errorf("failed to encode png: %v", err)
    }
    if err := bufWriter.Flush(); err != nil {
        return fmt.Errorf("failed to flush output: %v", err)
    }
    fmt.Printf("PNG Encoding Time: %v\n", time.Since(pngStart))
    
    fmt.Println("Success.")
    return nil
}

// upsamplePlane expands dimensions by 2x using Bilinear Interpolation
func upsamplePlane(src *image.Gray, targetW, targetH int) *image.Gray {
    dst := image.NewGray(image.Rect(0, 0, targetW, targetH))
    srcBounds := src.Bounds()
    srcW, srcH := srcBounds.Dx(), srcBounds.Dy()
    
    parallelUpsample(src, dst, srcW, srcH, targetW, targetH)
    return dst
}

func parallelUpsample(src, dst *image.Gray, srcW, srcH, dstW, dstH int) {
    var wg sync.WaitGroup
    workers := runtime.NumCPU()
    rowsPerWorker := dstH / workers
    if rowsPerWorker < 1 { rowsPerWorker = 1 }
    
    for i := 0; i < workers; i++ {
        startY := i * rowsPerWorker
        endY := startY + rowsPerWorker
        if i == workers-1 { endY = dstH }
        
        wg.Add(1)
        go func(y0, y1 int) {
            defer wg.Done()
            for y := y0; y < y1; y++ {
                // Map target y to source y
                srcFy := float32(y) * (float32(srcH) / float32(dstH))
                yLow := int(srcFy)
                yHigh := yLow + 1
                if yHigh >= srcH { yHigh = srcH - 1 }
                yWeight := srcFy - float32(yLow)
                
                row := dst.Pix[y*dst.Stride:] 
                
                for x := 0; x < dstW; x++ {
                     // Map target x to source x
                    srcFx := float32(x) * (float32(srcW) / float32(dstW))
                    xLow := int(srcFx)
                    xHigh := xLow + 1
                    if xHigh >= srcW { xHigh = srcW - 1 }
                    xWeight := srcFx - float32(xLow)
                    
                    // Bilinear interpolation
                    p00 := float32(src.GrayAt(xLow, yLow).Y)
                    p10 := float32(src.GrayAt(xHigh, yLow).Y)
                    p01 := float32(src.GrayAt(xLow, yHigh).Y)
                    p11 := float32(src.GrayAt(xHigh, yHigh).Y)
                    
                    top := p00*(1-xWeight) + p10*xWeight
                    bottom := p01*(1-xWeight) + p11*xWeight
                    val := top*(1-yWeight) + bottom*yWeight
                    
                    row[x] = uint8(val)
                }
            }
        }(startY, endY)
    }
    wg.Wait()
}

// fillPlane initializes an image with a constant value
func fillPlane(img *image.Gray, val uint8) {
	for i := range img.Pix {
		img.Pix[i] = val
	}
}

// Optimized plane decoder with batch reading
func gapDecodePlaneOptimized(reader io.Reader, width, height int, flags uint32, initVal uint8, s_val float32) (*image.Gray, error) {
    paddedW := (width + 7) / 8 * 8
    paddedH := (height + 7) / 8 * 8
    
    img := image.NewGray(image.Rect(0, 0, width, height))
    fillPlane(img, initVal)
    
    isQuantized := (flags & 2) != 0
    
    // Preallocate read buffer for batch reading
    headerBuf := make([]byte, 6) // angle(1) + count(1) + maxVal(4)
    
    processed := 0
    for y := 0; y < paddedH; y += 8 {
        for x := 0; x < paddedW; x += 8 {
            // Batch read header
            if isQuantized {
                if _, err := io.ReadFull(reader, headerBuf); err != nil {
                    return nil, fmt.Errorf("failed to read header at patch %d: %v", processed, err)
                }
            } else {
                if _, err := io.ReadFull(reader, headerBuf[:2]); err != nil {
                    return nil, fmt.Errorf("failed to read header at patch %d: %v", processed, err)
                }
            }
            
            byteAngle := headerBuf[0]
            byteCount := headerBuf[1]
            angle := float32(byteAngle) / 255.0 * 2.0 * math.Pi
            
            // Get coeffs from pool
            coeffs := coeffPool.Get().([]float32)
            for i := range coeffs { coeffs[i] = 0 }
            
            var maxVal float32 = 1.0
            if isQuantized {
                maxVal = math.Float32frombits(binary.LittleEndian.Uint32(headerBuf[2:6]))
            }
            
            // Batch read all coefficients for this patch
            coeffCount := int(byteCount)
            if coeffCount > 0 {
                coeffBuf := make([]byte, coeffCount*3) // idx + re + im per coeff
                if _, err := io.ReadFull(reader, coeffBuf); err != nil {
                    return nil, fmt.Errorf("failed to read coeffs at patch %d: %v", processed, err)
                }
                
                for k := 0; k < coeffCount; k++ {
                    idx := coeffBuf[k*3]
                    qRe := int8(coeffBuf[k*3+1])
                    qIm := int8(coeffBuf[k*3+2])
                    
                    re := float32(qRe) / 127.0 * maxVal
                    im := float32(qIm) / 127.0 * maxVal
                    
                    if int(idx) < 64 {
                        coeffs[2*int(idx)] = re
                        coeffs[2*int(idx)+1] = im
                    }
                }
            }
            
            // Decompress via Zig FFT
            patchBuffer := make([]float32, 64)
            if err := GapDecompressPatchTo(coeffs, angle, s_val, patchBuffer); err != nil {
                return nil, fmt.Errorf("failed to decompress patch %d: %v", processed, err)
            }
            
            // Write to Image
            for py := 0; py < 8; py++ {
                for px := 0; px < 8; px++ {
                    destX := x + px
                    destY := y + py
                    
                    if destX < width && destY < height {
                        val := patchBuffer[py*8+px]
                        if val < 0 { val = 0 }
                        if val > 1 { val = 1 }
                        
                        gray := uint8(val * 255.0)
                        img.Pix[destY*img.Stride+destX] = gray // Direct access
                    }
                }
            }
            coeffPool.Put(coeffs)
            processed++
        }
    }
    return img, nil
}

// gapDecodePlaneSplit decodes from 5 separate streams with parallel math
func gapDecodePlaneSplit(angles, counts, maxVals, indices, values []byte, width, height int, flags uint32, initVal uint8, s_val float32) (*image.Gray, error) {
    paddedW := (width + 7) / 8 * 8
    paddedH := (height + 7) / 8 * 8
    
    img := image.NewGray(image.Rect(0, 0, width, height))
    fillPlane(img, initVal)
    
    // 1. Pre-calculate number of patches
    numPatches := (paddedW / 8) * (paddedH / 8)
    
    // 2. Pre-allocate buffers for parallel work
    // 565k patches * 128 floats = ~290MB. 
    allCoeffs := make([]float32, numPatches * 128)
    allAngles := make([]float32, numPatches)
    coords := make([]struct{x, y int}, numPatches)
    
    // 3. Sequential stage: Parse streams (very fast)
    ptrA, ptrC, ptrMax, ptrIdx, ptrVal := 0, 0, 0, 0, 0
    pIdx := 0
    for y := 0; y < paddedH; y += 8 {
        for x := 0; x < paddedW; x += 8 {
            if ptrA >= len(angles) || ptrC >= len(counts) || pIdx >= numPatches { break }
            
            byteAngle := angles[ptrA]; ptrA++
            byteCount := counts[ptrC]; ptrC++
            
            angle := float32(byteAngle) / 255.0 * 2.0 * math.Pi
            allAngles[pIdx] = angle
            coords[pIdx].x = x
            coords[pIdx].y = y
            
            // Read MaxVal
            var maxVal float32 = 1.0
            if ptrMax+4 <= len(maxVals) {
                bits := binary.LittleEndian.Uint32(maxVals[ptrMax : ptrMax+4])
                maxVal = math.Float32frombits(bits)
                ptrMax += 4
            }
            
            // Populate Coeffs slice from flat buffer
            fCoeffs := allCoeffs[pIdx*128 : (pIdx+1)*128]
            count := int(byteCount)
            for k := 0; k < count; k++ {
                if ptrIdx >= len(indices) || ptrVal+1 >= len(values) { break }
                idx := indices[ptrIdx]; ptrIdx++
                qRe := int8(values[ptrVal])
                qIm := int8(values[ptrVal+1]); ptrVal += 2
                
                if int(idx) < 64 {
                    fCoeffs[2*int(idx)] = float32(qRe) / 127.0 * maxVal
                    fCoeffs[2*int(idx)+1] = float32(qIm) / 127.0 * maxVal
                }
            }
            pIdx++
        }
    }
    
    // 4. Parallel stage: Math + Reconstruction
    if pIdx > 0 {
        numWorkers := runtime.NumCPU()
        if numWorkers > pIdx { numWorkers = pIdx }
        
        var wg sync.WaitGroup
        chunkSize := (pIdx + numWorkers - 1) / numWorkers
        
        for w := 0; w < numWorkers; w++ {
            start := w * chunkSize
            end := start + chunkSize
            if end > pIdx { end = pIdx }
            if start >= end { continue }
            
            wg.Add(1)
            go func(s, e int) {
                defer wg.Done()
                
                // 1. Bulk decompress entire chunk in one CGO call
                chunkPatches := e - s
                chunkCoeffs := allCoeffs[s*128 : e*128]
                chunkAngles := allAngles[s : e]
                pixelBuf := make([]float32, chunkPatches * 64)
                
                if err := GapDecompressPatches(chunkCoeffs, chunkAngles, pixelBuf, s_val); err != nil {
                    // We can't return an error easily from a goroutine without a channel,
                    // but for production hardening we should log and maybe use a sync-once error.
                    // For now, let's just log and ensure we don't panic.
                    fmt.Printf("Error: bulk decompression failed: %v\n", err)
                    return 
                }
                
                // 2. Parallel write to Image
                for i := 0; i < chunkPatches; i++ {
                    pIdx := s + i
                    x, y := coords[pIdx].x, coords[pIdx].y
                    patch := pixelBuf[i*64 : (i+1)*64]
                    
                    for py := 0; py < 8; py++ {
                        for px := 0; px < 8; px++ {
                            origX := x + px
                            origY := y + py
                            if origX < width && origY < height {
                                val := patch[py*8+px]
                                if val < 0 { val = 0 }
                                if val > 1 { val = 1 }
                                img.Pix[origY*img.Stride+origX] = uint8(val * 255.0)
                            }
                        }
                    }
                }
            }(start, end)
        }
        wg.Wait()
    }
    
    return img, nil
}

// DeblockImageParallel applies deblocking with parallel horizontal/vertical passes
func DeblockImageParallel(img *image.RGBA) {
    bounds := img.Bounds()
    w, h := bounds.Dx(), bounds.Dy()
    
    const (
        Beta          = 12 // More sensitive flatness check for fine lines
        NormThreshold = 30 // Raised: avoids oversmoothing sharp edges
        HighThreshold = 45 // Tuned: Balance between deblocking and texture
    )
    
    abs := func(x int) int { if x < 0 { return -x }; return x }
    max3 := func(a, b, c int) int { m := a; if b > m { m = b }; if c > m { m = c }; return m }
    
    diff := func(c1R, c1G, c1B, c2R, c2G, c2B uint8) int {
        return max3(abs(int(c1R)-int(c2R)), abs(int(c1G)-int(c2G)), abs(int(c1B)-int(c2B)))
    }
    
    smooth := func(v_p2, v_p1, v_q0, v_q1 uint8) (uint8, uint8) {
        val_p1 := (int(v_p2) + 2*int(v_p1) + int(v_q0) + 2) / 4
        val_q0 := (int(v_p1) + 2*int(v_q0) + int(v_q1) + 2) / 4
        return uint8(val_p1), uint8(val_q0)
    }
    
    numWorkers := runtime.NumCPU()
    var wg sync.WaitGroup
    
    // Vertical edges - parallelize by edge columns
    edges := make([]int, 0)
    for x := 8; x < w-1; x += 8 {
        edges = append(edges, x)
    }
    
    edgesPerWorker := (len(edges) + numWorkers - 1) / numWorkers
    for w := 0; w < numWorkers && w*edgesPerWorker < len(edges); w++ {
        startIdx := w * edgesPerWorker
        endIdx := startIdx + edgesPerWorker
        if endIdx > len(edges) { endIdx = len(edges) }
        
        wg.Add(1)
        go func(edgeSlice []int) {
            defer wg.Done()
            for _, x := range edgeSlice {
                for y := 0; y < h; y++ {
                    idx_p2 := img.PixOffset(x-2, y)
                    idx_p1 := img.PixOffset(x-1, y)
                    idx_q0 := img.PixOffset(x, y)
                    idx_q1 := img.PixOffset(x+1, y)
                    
                    p2R, p2G, p2B := img.Pix[idx_p2], img.Pix[idx_p2+1], img.Pix[idx_p2+2]
                    p1R, p1G, p1B := img.Pix[idx_p1], img.Pix[idx_p1+1], img.Pix[idx_p1+2]
                    q0R, q0G, q0B := img.Pix[idx_q0], img.Pix[idx_q0+1], img.Pix[idx_q0+2]
                    q1R, q1G, q1B := img.Pix[idx_q1], img.Pix[idx_q1+1], img.Pix[idx_q1+2]
                    
                    flatP := diff(p2R, p2G, p2B, p1R, p1G, p1B) < Beta
                    flatQ := diff(q0R, q0G, q0B, q1R, q1G, q1B) < Beta
                    
                    threshold := NormThreshold
                    if flatP && flatQ { threshold = HighThreshold }
                    
                    if diff(p1R, p1G, p1B, q0R, q0G, q0B) < threshold {
                        r1, r0 := smooth(p2R, p1R, q0R, q1R)
                        g1, g0 := smooth(p2G, p1G, q0G, q1G)
                        b1, b0 := smooth(p2B, p1B, q0B, q1B)
                        
                        img.Pix[idx_p1], img.Pix[idx_p1+1], img.Pix[idx_p1+2] = r1, g1, b1
                        img.Pix[idx_q0], img.Pix[idx_q0+1], img.Pix[idx_q0+2] = r0, g0, b0
                    }
                }
            }
        }(edges[startIdx:endIdx])
    }
    wg.Wait()
    
    // Horizontal edges - parallelize by edge rows
    hEdges := make([]int, 0)
    for y := 8; y < h-1; y += 8 {
        hEdges = append(hEdges, y)
    }
    
    hEdgesPerWorker := (len(hEdges) + numWorkers - 1) / numWorkers
    for wk := 0; wk < numWorkers && wk*hEdgesPerWorker < len(hEdges); wk++ {
        startIdx := wk * hEdgesPerWorker
        endIdx := startIdx + hEdgesPerWorker
        if endIdx > len(hEdges) { endIdx = len(hEdges) }
        
        wg.Add(1)
        go func(edgeSlice []int) {
            defer wg.Done()
            for _, y := range edgeSlice {
                for x := 0; x < w; x++ {
                    idx_p2 := img.PixOffset(x, y-2)
                    idx_p1 := img.PixOffset(x, y-1)
                    idx_q0 := img.PixOffset(x, y)
                    idx_q1 := img.PixOffset(x, y+1)
                    
                    p2R, p2G, p2B := img.Pix[idx_p2], img.Pix[idx_p2+1], img.Pix[idx_p2+2]
                    p1R, p1G, p1B := img.Pix[idx_p1], img.Pix[idx_p1+1], img.Pix[idx_p1+2]
                    q0R, q0G, q0B := img.Pix[idx_q0], img.Pix[idx_q0+1], img.Pix[idx_q0+2]
                    q1R, q1G, q1B := img.Pix[idx_q1], img.Pix[idx_q1+1], img.Pix[idx_q1+2]
                    
                    flatP := diff(p2R, p2G, p2B, p1R, p1G, p1B) < Beta
                    flatQ := diff(q0R, q0G, q0B, q1R, q1G, q1B) < Beta
                    
                    threshold := NormThreshold
                    if flatP && flatQ { threshold = HighThreshold }
                    
                    if diff(p1R, p1G, p1B, q0R, q0G, q0B) < threshold {
                        r1, r0 := smooth(p2R, p1R, q0R, q1R)
                        g1, g0 := smooth(p2G, p1G, q0G, q1G)
                        b1, b0 := smooth(p2B, p1B, q0B, q1B)
                        
                        img.Pix[idx_p1], img.Pix[idx_p1+1], img.Pix[idx_p1+2] = r1, g1, b1
                        img.Pix[idx_q0], img.Pix[idx_q0+1], img.Pix[idx_q0+2] = r0, g0, b0
                    }
                }
            }
        }(hEdges[startIdx:endIdx])
    }
    wg.Wait()
}

// applyEdgeAntialiasing uses Directional Guided Antialiasing (DGAA)
// It detects edge orientation via Sobel and smooths ALONG the edge, not across it.
func applyEdgeAntialiasing(img *image.RGBA) {
    bounds := img.Bounds()
    w, h := bounds.Dx(), bounds.Dy()
    out := image.NewRGBA(bounds)
    copy(out.Pix, img.Pix)
    
    const (
        EdgeThreshold    = 30  // Adjusted: ignore very faint noise, focus on real edges
        ImpulseThreshold = 100 // Threshold for detecting isolated dots
    )
    
    abs := func(x int) int { if x < 0 { return -x }; return x }
    numWorkers := runtime.NumCPU()
    var wg sync.WaitGroup
    
    rowsPerWorker := (h - 2 + numWorkers - 1) / numWorkers
    for wk := 0; wk < numWorkers; wk++ {
        startY := 1 + wk*rowsPerWorker
        endY := startY + rowsPerWorker
        if endY > h-1 { endY = h - 1 }
        if startY >= endY { break }
        
        wg.Add(1)
        go func(yMin, yMax int) {
            defer wg.Done()
            for y := yMin; y < yMax; y++ {
                for x := 1; x < w-1; x++ {
                    idx := img.PixOffset(x, y)
                    pR, pG, pB := img.Pix[idx], img.Pix[idx+1], img.Pix[idx+2]
                    
                    // 1. Impulse Noise Rejection (Despeckle)
                    isDot := true
                    var rAvg, gAvg, bAvg, neighbors int
                    for dy := -1; dy <= 1; dy++ {
                        for dx := -1; dx <= 1; dx++ {
                            if dx == 0 && dy == 0 { continue }
                            nIdx := img.PixOffset(x+dx, y+dy)
                            nR, nG, nB := img.Pix[nIdx], img.Pix[nIdx+1], img.Pix[nIdx+2]
                            diff := (abs(int(pR)-int(nR)) + abs(int(pG)-int(nG)) + abs(int(pB)-int(nB))) / 3
                            if diff < ImpulseThreshold {
                                isDot = false
                            }
                            rAvg += int(nR); gAvg += int(nG); bAvg += int(nB)
                            neighbors++
                        }
                    }
                    
                    if isDot {
                        out.Pix[idx] = uint8(rAvg / neighbors)
                        out.Pix[idx+1] = uint8(gAvg / neighbors)
                        out.Pix[idx+2] = uint8(bAvg / neighbors)
                        continue 
                    }
                    
                    // 2. DGAA: Directional Guided Antialiasing
                    // Compute Sobel gradients to find edge direction
                    // Sobel X: [-1 0 +1; -2 0 +2; -1 0 +1]
                    // Sobel Y: [-1 -2 -1; 0 0 0; +1 +2 +1]
                    var gx, gy int
                    for c := 0; c < 3; c++ { // Sum over R, G, B
                        p00 := int(img.Pix[img.PixOffset(x-1, y-1)+c])
                        p10 := int(img.Pix[img.PixOffset(x, y-1)+c])
                        p20 := int(img.Pix[img.PixOffset(x+1, y-1)+c])
                        p01 := int(img.Pix[img.PixOffset(x-1, y)+c])
                        p21 := int(img.Pix[img.PixOffset(x+1, y)+c])
                        p02 := int(img.Pix[img.PixOffset(x-1, y+1)+c])
                        p12 := int(img.Pix[img.PixOffset(x, y+1)+c])
                        p22 := int(img.Pix[img.PixOffset(x+1, y+1)+c])
                        
                        gx += (-p00 + p20 - 2*p01 + 2*p21 - p02 + p22)
                        gy += (-p00 - 2*p10 - p20 + p02 + 2*p12 + p22)
                    }
                    gx /= 3
                    gy /= 3
                    
                    gradMag := int(math.Sqrt(float64(gx*gx + gy*gy)))
                    
                    if gradMag > EdgeThreshold {
                        // Smooth ALONG the edge (perpendicular to gradient)
                        // Edge direction is (-gy, gx), normalized
                        // We pick the two neighbors along this direction.
                        var dx1, dy1, dx2, dy2 int
                        if abs(gx) > abs(gy) {
                            // Gradient is mostly horizontal -> edge is vertical
                            // Smooth along Y axis (neighbors above/below)
                            dx1, dy1 = 0, -1
                            dx2, dy2 = 0, 1
                        } else {
                            // Gradient is mostly vertical -> edge is horizontal
                            // Smooth along X axis (neighbors left/right)
                            dx1, dy1 = -1, 0
                            dx2, dy2 = 1, 0
                        }
                        
                        n1Idx := img.PixOffset(x+dx1, y+dy1)
                        n2Idx := img.PixOffset(x+dx2, y+dy2)
                        
                        // Weighted average: center=2, neighbors=1 each
                        out.Pix[idx] = uint8((2*int(pR) + int(img.Pix[n1Idx]) + int(img.Pix[n2Idx])) / 4)
                        out.Pix[idx+1] = uint8((2*int(pG) + int(img.Pix[n1Idx+1]) + int(img.Pix[n2Idx+1])) / 4)
                        out.Pix[idx+2] = uint8((2*int(pB) + int(img.Pix[n1Idx+2]) + int(img.Pix[n2Idx+2])) / 4)
                    }
                }
            }
        }(startY, endY)
    }
    wg.Wait()
    copy(img.Pix, out.Pix)
}

// Keep old function for backward compatibility if needed
func DeblockImage(img *image.RGBA) {
    DeblockImageParallel(img)
}

// applyLineContinuityFilter applies multi-pass bilateral filtering at block seams
// This aggressively smooths block boundary artifacts while preserving overall contrast
func applyLineContinuityFilter(img *image.RGBA) {
    bounds := img.Bounds()
    w, h := bounds.Dx(), bounds.Dy()
    
    const (
        BlockSize    = 8
        SeamRadius   = 2   // Apply filter within this many pixels of block seams
        FilterRadius = 3   // Bilateral filter kernel radius
        SigmaSpace   = 2.0 // Spatial sigma
        SigmaColor   = 22.0 // Increased: better hiding of block edges
        NumPasses    = 2    // Two passes to target stubborn blocks
    )
    
    // Pre-compute spatial weights
    spatialWeights := make([]float64, (2*FilterRadius+1)*(2*FilterRadius+1))
    for dy := -FilterRadius; dy <= FilterRadius; dy++ {
        for dx := -FilterRadius; dx <= FilterRadius; dx++ {
            dist := math.Sqrt(float64(dx*dx + dy*dy))
            spatialWeights[(dy+FilterRadius)*(2*FilterRadius+1)+(dx+FilterRadius)] = math.Exp(-dist * dist / (2 * SigmaSpace * SigmaSpace))
        }
    }
    
    isNearSeam := func(x, y int) bool {
        xMod := x % BlockSize
        yMod := y % BlockSize
        nearX := xMod < SeamRadius || xMod >= (BlockSize-SeamRadius)
        nearY := yMod < SeamRadius || yMod >= (BlockSize-SeamRadius)
        return nearX || nearY
    }
    
    numWorkers := runtime.NumCPU()
    
    for pass := 0; pass < NumPasses; pass++ {
        out := image.NewRGBA(bounds)
        copy(out.Pix, img.Pix)
        
        var wg sync.WaitGroup
        rowsPerWorker := (h + numWorkers - 1) / numWorkers
        
        for wk := 0; wk < numWorkers; wk++ {
            startY := wk * rowsPerWorker
            endY := startY + rowsPerWorker
            if endY > h { endY = h }
            if startY >= endY { break }
            
            wg.Add(1)
            go func(yMin, yMax int) {
                defer wg.Done()
                for y := yMin; y < yMax; y++ {
                    for x := 0; x < w; x++ {
                        if !isNearSeam(x, y) { continue }
                        
                        idx := img.PixOffset(x, y)
                        pR, pG, pB := float64(img.Pix[idx]), float64(img.Pix[idx+1]), float64(img.Pix[idx+2])
                        
                        var rSum, gSum, bSum, wSum float64
                        
                        for dy := -FilterRadius; dy <= FilterRadius; dy++ {
                            ny := y + dy
                            if ny < 0 || ny >= h { continue }
                            
                            for dx := -FilterRadius; dx <= FilterRadius; dx++ {
                                nx := x + dx
                                if nx < 0 || nx >= w { continue }
                                
                                nIdx := img.PixOffset(nx, ny)
                                nR, nG, nB := float64(img.Pix[nIdx]), float64(img.Pix[nIdx+1]), float64(img.Pix[nIdx+2])
                                
                                // Color distance
                                colorDist := math.Sqrt((pR-nR)*(pR-nR) + (pG-nG)*(pG-nG) + (pB-nB)*(pB-nB))
                                colorWeight := math.Exp(-colorDist * colorDist / (2 * SigmaColor * SigmaColor))
                                
                                // Spatial weight (precomputed)
                                spIdx := (dy+FilterRadius)*(2*FilterRadius+1) + (dx+FilterRadius)
                                weight := spatialWeights[spIdx] * colorWeight
                                
                                rSum += nR * weight
                                gSum += nG * weight
                                bSum += nB * weight
                                wSum += weight
                            }
                        }
                        
                        if wSum > 0 {
                            out.Pix[idx] = uint8(rSum / wSum)
                            out.Pix[idx+1] = uint8(gSum / wSum)
                            out.Pix[idx+2] = uint8(bSum / wSum)
                        }
                    }
                }
            }(startY, endY)
        }
        wg.Wait()
        copy(img.Pix, out.Pix)
    }
}

