package main

import (
    "bytes"
    "encoding/binary"
    "fmt"
    "image"
    "image/color"
    _ "image/jpeg"
    _ "image/png"
    "math"
    "os"
    "sync"
    "runtime"
)

var patchPool = sync.Pool{
	New: func() any {
		return make([]float32, 64)
	},
}

// GapHeader structure matches the spec
type GapHeader struct {
    Magic     [4]byte
    Width     uint32
    Height    uint32
    S         float32
    Threshold float32
    Flags     uint32
    Channels  uint32 // New for v1.4
}

func EncodeImage(inputPath, outputPath string, s, threshold float32) error {
    // 1. Load Image
    file, err := os.Open(inputPath)
    if err != nil {
        return fmt.Errorf("failed to open input: %v", err)
    }
    defer file.Close()

    srcImg, _, err := image.Decode(file)
    if err != nil {
        return fmt.Errorf("failed to decode image: %v", err)
    }

    bounds := srcImg.Bounds()
    width := bounds.Dx()
    height := bounds.Dy()
    
    fmt.Printf("Encoding %s (%dx%d) -> %s (YCbCr)\n", inputPath, width, height, outputPath)

    // 2. Prepare Planes (Y, Cb, Cr)
    yPlane := image.NewGray(bounds)
    cbPlane := image.NewGray(bounds)
    crPlane := image.NewGray(bounds)
    
    for y := 0; y < height; y++ {
        for x := 0; x < width; x++ {
            r, g, b, _ := srcImg.At(bounds.Min.X + x, bounds.Min.Y + y).RGBA()
            yy, cb, cr := color.RGBToYCbCr(uint8(r>>8), uint8(g>>8), uint8(b>>8))
            
            yPlane.SetGray(bounds.Min.X + x, bounds.Min.Y + y, color.Gray{Y: yy})
            cbPlane.SetGray(bounds.Min.X + x, bounds.Min.Y + y, color.Gray{Y: cb})
            crPlane.SetGray(bounds.Min.X + x, bounds.Min.Y + y, color.Gray{Y: cr})
        }
    }

    // 3. Open Output
    outFile, err := os.Create(outputPath)
    if err != nil {
        return fmt.Errorf("failed to create output: %v", err)
    }
    defer outFile.Close()

    // 4. Write Header
    header := GapHeader{
        Magic:     [4]byte{'G', 'A', 'P', 0x01},
        Width:     uint32(width),
        Height:    uint32(height),
        S:         s,
        Threshold: threshold,
        Flags:     2 | 4 | 8, // Quantized(2) | Subsampled(4) | Range(8)
        Channels:  3, // YCbCr
    }
    
    if err := binary.Write(outFile, binary.LittleEndian, &header); err != nil {
        return fmt.Errorf("failed to write header: %v", err)
    }

    // 5. Encode planes IN PARALLEL for speed
    runtime.GOMAXPROCS(runtime.NumCPU())
    

    
    // Downsample Chroma Planes (4:2:0)
    cbSmall := downsamplePlane(cbPlane)
    crSmall := downsamplePlane(crPlane)
    
    planes := []*image.Gray{yPlane, cbSmall, crSmall}
    
    // Chroma channels: Derived from input parameters
    // Factor 0.4 roughly matches the optimized 0.04/0.22 ratio for base defaults (s=0.1, t=0.5)
    chromaS := s * 0.4         
    chromaThreshold := threshold * 0.44 
    
    sValues := []float32{s, chromaS, chromaS}
    threshValues := []float32{threshold, chromaThreshold, chromaThreshold}
    
    type planeResult struct {
        angles  []byte
        counts  []byte
        maxVals []byte
        indices []byte
        values  []byte
        err     error
    }
    
    results := make([]planeResult, 3)
    var wg sync.WaitGroup
    
    for i := 0; i < 3; i++ {
        wg.Add(1)
        go func(idx int) {
            defer wg.Done()
            // Use actual dimensions
            p := planes[idx]
            pBounds := p.Bounds()
            
            // Generate Split Streams
            angles, counts, maxVals, indices, values, err := gapEncodePlane(p, pBounds.Dx(), pBounds.Dy(), sValues[idx], threshValues[idx])
            results[idx] = planeResult{angles: angles, counts: counts, maxVals: maxVals, indices: indices, values: values, err: err}
        }(i)
    }
    
    wg.Wait()
    
    // Check for errors
    for i, r := range results {
        if r.err != nil { return fmt.Errorf("failed to encode plane %d: %v", i, r.err) }
    }
    
    // 6. Write Compressed Data (Range Coded Split Streams)
    // Order: Angles, Counts, MaxVals, Indices, Values
    for i := 0; i < 3; i++ {
        // Helper to Compress and Write
        writeStream := func(name string, data []byte) error {
            uncompressedLen := uint32(len(data))
            
            compressed := GapCompressData(data)
            if compressed == nil {
                 if uncompressedLen == 0 {
                    binary.Write(outFile, binary.LittleEndian, uint32(0)) // U
                    binary.Write(outFile, binary.LittleEndian, uint32(0)) // C
                    return nil
                 }
                return fmt.Errorf("failed to compress %s for plane %d", name, i)
            }
            compressedLen := uint32(len(compressed))
            
            if err := binary.Write(outFile, binary.LittleEndian, uncompressedLen); err != nil { return err }
            if err := binary.Write(outFile, binary.LittleEndian, compressedLen); err != nil { return err }
            if _, err := outFile.Write(compressed); err != nil { return err }
            
            return nil
        }
        
        if err := writeStream("Angles", results[i].angles); err != nil { return err }
        if err := writeStream("Counts", results[i].counts); err != nil { return err }
        if err := writeStream("MaxVals", results[i].maxVals); err != nil { return err }
        if err := writeStream("Indices", results[i].indices); err != nil { return err }
        if err := writeStream("Values", results[i].values); err != nil { return err }
        
        rawTotal := len(results[i].angles) + len(results[i].counts) + len(results[i].maxVals) + len(results[i].indices) + len(results[i].values)
        fmt.Printf("Plane %d Raw: %d bytes\n", i, rawTotal)
    }
    
    return nil
}

// downsamplePlane reduces dimensions by 2x using 2x2 averaging
func downsamplePlane(src *image.Gray) *image.Gray {
    b := src.Bounds()
    w, h := b.Dx(), b.Dy()
    newW, newH := w/2, h/2
    dst := image.NewGray(image.Rect(0, 0, newW, newH))
    
    for y := 0; y < newH; y++ {
        for x := 0; x < newW; x++ {
            // Average 2x2 block with clamping for odd dimensions
            srcX, srcY := x*2, y*2
            x2 := srcX + 1
            y2 := srcY + 1
            if x2 >= w { x2 = w - 1 }
            if y2 >= h { y2 = h - 1 }

            sum := int(src.GrayAt(srcX, srcY).Y) +
                   int(src.GrayAt(x2, srcY).Y) +
                   int(src.GrayAt(srcX, y2).Y) +
                   int(src.GrayAt(x2, y2).Y)
            dst.SetGray(x, y, color.Gray{Y: uint8(sum / 4)})
        }
    }
    return dst
}

// gapEncodePlane encodes a single grayscale plane into split streams
func gapEncodePlane(img *image.Gray, width, height int, s, threshold float32) ([]byte, []byte, []byte, []byte, []byte, error) {
    paddedW := (width + 7) / 8 * 8
    paddedH := (height + 7) / 8 * 8
    
    // Estimate sizes
    numPatches := (paddedW / 8) * (paddedH / 8)
    angles := make([]byte, 0, numPatches)
    counts := make([]byte, 0, numPatches)
    maxVals := make([]byte, 0, numPatches * 4) // float32
    indices := make([]byte, 0, numPatches * 16)
    values := make([]byte, 0, numPatches * 32)
    
    // Buffers for binary writing
    maxValBuf := new(bytes.Buffer)
    
    for y := 0; y < paddedH; y += 8 {
        for x := 0; x < paddedW; x += 8 {
            patchBuffer := patchPool.Get().([]float32)
            
            // Fill patch buffer with edge clamping padding
            for py := 0; py < 8; py++ {
                origY := y + py
                if origY >= height { origY = height - 1 }
                
                for px := 0; px < 8; px++ {
                    origX := x + px
                    if origX >= width { origX = width - 1 }
                    
                    val := float32(img.GrayAt(origX, origY).Y) / 255.0
                    patchBuffer[py*8+px] = val
                }
            }
            
            // Compress
            angle, cCoeffs, _, err := GapCompressPatch(patchBuffer, s, threshold)
            if err != nil {
                return nil, nil, nil, nil, nil, fmt.Errorf("failed to compress patch at (%d, %d): %v", x, y, err)
            }
            
            // Quantize Angle
            normAngle := float64(angle)
            for normAngle < 0 { normAngle += 2 * math.Pi }
            byteAngle := uint8((normAngle / (2 * math.Pi)) * 255.0)

            // Find MaxVal
            var maxVal float32 = 0
            for k := 0; k < 64; k++ {
                re := cCoeffs[2*k]
                im := cCoeffs[2*k+1]
                mag := math.Sqrt(float64(re*re + im*im))
                if mag > 0 {
                    if float32(math.Abs(float64(re))) > maxVal { maxVal = float32(math.Abs(float64(re))) }
                    if float32(math.Abs(float64(im))) > maxVal { maxVal = float32(math.Abs(float64(im))) }
                }
            }
            if maxVal == 0 { maxVal = 1.0 }

            actualCount := 0
            for k := 0; k < 64; k++ {
                re := cCoeffs[2*k]
                im := cCoeffs[2*k+1]
                mag := math.Sqrt(float64(re*re + im*im))
                
                if mag > 0 { 
                     indices = append(indices, uint8(k))
                     qRe := int8(re / maxVal * 127.0)
                     qIm := int8(im / maxVal * 127.0)
                     values = append(values, byte(qRe), byte(qIm))
                     actualCount++
                }
            }

            // Append to streams
            angles = append(angles, byteAngle)
            counts = append(counts, uint8(actualCount))
            
            maxValBuf.Reset()
            binary.Write(maxValBuf, binary.LittleEndian, maxVal)
            maxVals = append(maxVals, maxValBuf.Bytes()...)

            patchPool.Put(patchBuffer)
        }
    }
    return angles, counts, maxVals, indices, values, nil
}
