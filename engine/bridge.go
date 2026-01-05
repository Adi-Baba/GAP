package main

/*
#cgo CFLAGS: -I.
#cgo LDFLAGS: -L. -lgap

#include <stdlib.h>

// Forward declarations of Zig exports
float gap_analyze_patch(const float* patch);
int gap_compress_patch(const float* input, float* output, float angle, float s, float threshold);
void gap_decompress_patch(const float* coeffs, float* output, float angle, float s_val);
void gap_decompress_patches(const float* coeffs, float* output, const float* angles, size_t num_patches, float s_val);
size_t gap_compress_data(const unsigned char* input, size_t input_len, unsigned char* output, size_t output_cap);
void gap_decompress_data(const unsigned char* input, size_t input_len, unsigned char* output, size_t output_len);
*/
import "C"
import (
    "fmt"
    "unsafe"
)

// GapCompressPatch ... (same as before)

// GapCompressData compresses a byte slice using Range Coding.
// Returns compressed bytes.
func GapCompressData(input []byte) []byte {
    if len(input) == 0 { return nil }
    
    // Output capacity: Input + slightly more for overhead (though usually smaller)
    // Range coding rarely expands unless random noise, but be safe.
    maxCap := len(input) + 1024 
    output := make([]byte, maxCap)
    
    cIn := (*C.uchar)(unsafe.Pointer(&input[0]))
    cOut := (*C.uchar)(unsafe.Pointer(&output[0]))
    
    written := C.gap_compress_data(cIn, C.size_t(len(input)), cOut, C.size_t(maxCap))
    
    if written == 0 {
        // Failed or empty
        return nil
    }
    
    return output[:written]
}

// GapDecompressData decompresses range-coded data.
// Output size MUST be correct.
func GapDecompressData(input []byte, outputSize int) []byte {
    if len(input) == 0 { return nil }
    
    output := make([]byte, outputSize)
    
    cIn := (*C.uchar)(unsafe.Pointer(&input[0]))
    cOut := (*C.uchar)(unsafe.Pointer(&output[0]))
    
    C.gap_decompress_data(cIn, C.size_t(len(input)), cOut, C.size_t(outputSize))
    
    return output
}

// GapCompressPatch analyzes and compresses an 8x8 patch.
// Returns: (angle, compressed_coeffs, keep_count, error)
func GapCompressPatch(patch []float32, s float32, threshold float32) (float32, []float32, int, error) {
    if len(patch) != 64 {
        return 0, nil, 0, fmt.Errorf("patch must be 64 floats, got %d", len(patch))
    }

    cInput := (*C.float)(unsafe.Pointer(&patch[0]))
    
    // 1. Analyze
    angle := float32(C.gap_analyze_patch(cInput))
    
    // 2. Compress
    output := make([]float32, 128)
    cOutput := (*C.float)(unsafe.Pointer(&output[0]))
    
    kept := int(C.gap_compress_patch(cInput, cOutput, C.float(angle), C.float(s), C.float(threshold)))
    
    return angle, output, kept, nil
}

func GapDecompressPatch(coeffs []float32, angle float32, s float32) ([]float32, error) {
    output := make([]float32, 64)
    if err := GapDecompressPatchTo(coeffs, angle, s, output); err != nil {
        return nil, err
    }
    return output, nil
}

func GapDecompressPatchTo(coeffs []float32, angle float32, s float32, output []float32) error {
    if (len(coeffs) != 128 || len(output) != 64) {
        return fmt.Errorf("invalid buffer sizes for GapDecompressPatch: coeffs=%d, output=%d", len(coeffs), len(output))
    }
    cCoeffs := (*C.float)(unsafe.Pointer(&coeffs[0]))
    cOutput := (*C.float)(unsafe.Pointer(&output[0]))
    C.gap_decompress_patch(cCoeffs, cOutput, C.float(angle), C.float(s))
    return nil
}

func GapDecompressPatches(coeffs []float32, angles []float32, output []float32, s float32) error {
    numPatches := len(angles)
    if numPatches == 0 { return nil }
    if (len(coeffs) < numPatches*128 || len(output) < numPatches*64) {
        return fmt.Errorf("batch buffer size mismatch: numPatches=%d, coeffs=%d, output=%d", numPatches, len(coeffs), len(output))
    }
    cCoeffs := (*C.float)(unsafe.Pointer(&coeffs[0]))
    cAngles := (*C.float)(unsafe.Pointer(&angles[0]))
    cOutput := (*C.float)(unsafe.Pointer(&output[0]))
    C.gap_decompress_patches(cCoeffs, cOutput, cAngles, C.size_t(numPatches), C.float(s))
    return nil
}
