const std = @import("std");
const gradient = @import("gradient.zig");
const pltm = @import("pltm.zig");
const entropy = @import("entropy.zig");

// --- C-ABI Exports ---

/// Analyzes a patch (64 floats) and returns the gradient angle in radians.
export fn gap_analyze_patch(patch_ptr: [*]const f32) f32 {
    const patch: *const [64]f32 = @ptrCast(patch_ptr);
    const result = gradient.compute_gradient(patch);
    return result.angle;
}

/// Applies Gradient Alignment + PLTM Transform.
/// Input: Raw Patch (64 floats).
/// Output: Compressed Coefficients (64 Complex floats, interleaved).
/// Returns: Number of non-zero coefficients kept.
export fn gap_compress_patch(
    input_ptr: [*]const f32, 
    output_ptr: [*]f32, // treated as [64]Complex
    angle: f32,
    s_val: f32,
    threshold: f32
) i32 {
    const input: *const [64]f32 = @ptrCast(input_ptr);
    const output: *[64]std.math.Complex(f32) = @ptrCast(output_ptr);
    
    // 1. Sort/Permute Input based on Angle (Gradient Alignment)
    var permuted_input: [64]f32 = undefined;
    var indices: [64]u8 = undefined;
    
    gradient.get_gradient_map(angle, &indices);
    
    for (indices, 0..) |idx, i| {
        permuted_input[i] = input[idx];
    }
    
    // 2. DFT + Filter
    pltm.apply_dft(&permuted_input, output);
    pltm.apply_polylog_filter(output, s_val);
    
    // 3. Compress
    const kept = pltm.compress_coeffs(output, threshold);
    return @intCast(kept);
}

/// Restores a patch from PLTM coefficients.
/// Input: Compressed Coefficients.
/// Output: Reconstructed Patch.
export fn gap_decompress_patch(
    coeffs_ptr: [*]const f32,
    output_ptr: [*]f32,
    angle: f32,
    s_val: f32
) void {
    var permuted_output: [64]f32 = undefined;
    const complex_coeffs: *[64]std.math.Complex(f32) = @constCast(@ptrCast(coeffs_ptr));
    
    // 1. Inverse Filter (Sharpening/Restoration)
    pltm.apply_inverse_polylog_filter(complex_coeffs, s_val);
    
    // 2. IDFT
    pltm.apply_idft(complex_coeffs, &permuted_output);
    
    // 3. Reverse Permutation (Inverse Map)
    const indices = gradient.get_gradient_map_ptr(angle);
    var i: usize = 0;
    while (i < 64) : (i += 1) {
        output_ptr[indices[i]] = permuted_output[i];
    }
}

/// Batch decompress patches to reduce CGO overhead.
export fn gap_decompress_patches(
    coeffs_ptr: [*]const f32, // [num_patches * 128]
    output_ptr: [*]f32,       // [num_patches * 64]
    angles_ptr: [*]const f32, // [num_patches]
    num_patches: usize,
    s_val: f32
) void {
    var i: usize = 0;
    while (i < num_patches) : (i += 1) {
        gap_decompress_patch(
            coeffs_ptr + i * 128,
            output_ptr + i * 64,
            angles_ptr[i],
            s_val
        );
    }
}

/// Compresses a buffer of bytes using Range Coding.
/// Returns number of bytes written to output.
export fn gap_compress_data(
    input_ptr: [*]const u8,
    input_len: usize,
    output_ptr: [*]u8,
    output_cap: usize
) usize {
    const input = input_ptr[0..input_len];
    
    // Use general purpose allocator for temporary buffer
    var gpa = std.heap.GeneralPurposeAllocator(.{}){};
    const allocator = gpa.allocator();
    
    var encoder = entropy.RangeEncoder.init(allocator);
    defer encoder.deinit();
    
    var model = entropy.FrequencyModel.init();
    
    for (input) |byte| {
        encoder.encode_safe(&model, byte) catch return 0;
    }
    encoder.finish() catch return 0;
    
    const compressed = encoder.buffer.items;
    if (compressed.len > output_cap) return 0;
    
    @memcpy(output_ptr[0..compressed.len], compressed);
    return compressed.len;
}

/// Decompresses data using Range Coding.
export fn gap_decompress_data(
    input_ptr: [*]const u8,
    input_len: usize,
    output_ptr: [*]u8,
    output_len: usize
) void {
    const input = input_ptr[0..input_len];
    const output = output_ptr[0..output_len];
    
    var decoder = entropy.RangeDecoder.init(input);
    var model = entropy.FrequencyModel.init();
    
    for (0..output_len) |i| {
        output[i] = decoder.decode(&model);
    }
}
