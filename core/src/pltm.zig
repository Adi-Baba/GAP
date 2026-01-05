const std = @import("std");
const math = std.math;

pub const PatchSize = 8;
pub const PixelCount = PatchSize * PatchSize; // 64
const Complex = std.math.Complex(f32);

// Bit-reversal table is constant for N=64
const bit_reverse_table: [64]u6 = blk: {
    var table: [64]u6 = undefined;
    for (0..64) |i| {
        var rev: u6 = 0;
        var val: u6 = @intCast(i);
        for (0..6) |_| {
            rev = (rev << 1) | (val & 1);
            val >>= 1;
        }
        table[i] = rev;
    }
    break :blk table;
};

// SIMD optimized twiddles
const TwiddleTable = struct {
    cos: [64]f32,
    sin: [64]f32,
};

fn computeTwiddles(comptime inverse: bool) TwiddleTable {
    var result: TwiddleTable = undefined;
    const sign: f32 = if (inverse) 1.0 else -1.0;
    var idx: usize = 0;
    var len: usize = 2;
    while (len <= 64) : (len <<= 1) {
        const half = len >> 1;
        const step = sign * 2.0 * math.pi / @as(f32, @floatFromInt(len));
        for (0..half) |k| {
            const angle = @as(f32, @floatFromInt(k)) * step;
            result.cos[idx] = @cos(angle);
            result.sin[idx] = @sin(angle);
            idx += 1;
        }
    }
    return result;
}

const forward_twiddles = computeTwiddles(false);
const inverse_twiddles = computeTwiddles(true);

/// Optimized FFT that works on flat buffers to leverage cache efficiency
fn fft_radix2_internal(re: []f32, im: []f32, comptime inverse: bool) void {
    const twiddles = if (inverse) inverse_twiddles else forward_twiddles;
    var tw_idx: usize = 0;
    var len: usize = 2;
    
    while (len <= 64) : (len <<= 1) {
        const half = len >> 1;
        if (half >= 4) {
            const V = @Vector(4, f32);
            var start: usize = 0;
            while (start < 64) : (start += len) {
                var k: usize = 0;
                while (k < half) : (k += 4) {
                    const tw_re = V{twiddles.cos[tw_idx+k], twiddles.cos[tw_idx+k+1], twiddles.cos[tw_idx+k+2], twiddles.cos[tw_idx+k+3]};
                    const tw_im = V{twiddles.sin[tw_idx+k], twiddles.sin[tw_idx+k+1], twiddles.sin[tw_idx+k+2], twiddles.sin[tw_idx+k+3]};
                    
                    const ur = @as(V, re[start+k..][0..4].*);
                    const ui = @as(V, im[start+k..][0..4].*);
                    const vr = @as(V, re[start+k+half..][0..4].*);
                    const vi = @as(V, im[start+k+half..][0..4].*);
                    
                    const tr = vr * tw_re - vi * tw_im;
                    const ti = vr * tw_im + vi * tw_re;
                    
                    @as(*[4]f32, @ptrCast(&re[start+k])).* = ur + tr;
                    @as(*[4]f32, @ptrCast(&im[start+k])).* = ui + ti;
                    @as(*[4]f32, @ptrCast(&re[start+k+half])).* = ur - tr;
                    @as(*[4]f32, @ptrCast(&im[start+k+half])).* = ui - ti;
                }
            }
        } else {
            var start: usize = 0;
            while (start < 64) : (start += len) {
                for (0..half) |k| {
                    const tr = re[start+k+half] * twiddles.cos[tw_idx+k] - im[start+k+half] * twiddles.sin[tw_idx+k];
                    const ti = re[start+k+half] * twiddles.sin[tw_idx+k] + im[start+k+half] * twiddles.cos[tw_idx+k];
                    re[start+k+half] = re[start+k] - tr;
                    im[start+k+half] = im[start+k] - ti;
                    re[start+k] += tr;
                    im[start+k] += ti;
                }
            }
        }
        tw_idx += half;
    }
}

pub fn apply_dft(input: *const [64]f32, output: *[64]Complex) void {
    var re: [64]f32 = undefined;
    var im: [64]f32 = std.mem.zeroes([64]f32);
    for (0..64) |i| re[bit_reverse_table[i]] = input[i];
    fft_radix2_internal(&re, &im, false);
    for (0..64) |i| output[i] = Complex.init(re[i], im[i]);
}

pub fn apply_idft(input: *const [64]Complex, output: *[64]f32) void {
    var re: [64]f32 = undefined;
    var im: [64]f32 = undefined;
    // Interleaved input complex -> SoA
    for (0..64) |i| {
        const j = bit_reverse_table[i];
        re[j] = input[i].re;
        im[j] = input[i].im;
    }
    fft_radix2_internal(&re, &im, true);
    // Normalize and extract real (imaginary component should be zero for symmetric input)
    const V = @Vector(4, f32);
    const n_inv = V{ 1.0 / 64.0, 1.0 / 64.0, 1.0 / 64.0, 1.0 / 64.0 };
    var i: usize = 0;
    while (i < 64) : (i += 4) {
        const v = @as(V, re[i..][0..4].*);
        @as(*[4]f32, @ptrCast(&output[i])).* = v * n_inv;
    }
}

const weight_table: [64][64]f32 = blk: {
    @setEvalBranchQuota(1000000);
    var table: [64][64]f32 = undefined;
    for (0..64) |s_idx| {
        const s = @as(f32, @floatFromInt(s_idx)) * 0.1;
        table[s_idx][0] = 1.0;
        for (1..64) |k| {
            table[s_idx][k] = math.pow(f32, @floatFromInt(k), -s);
        }
    }
    break :blk table;
};

const inv_weight_table: [64][64]f32 = blk: {
    @setEvalBranchQuota(1000000);
    var table: [64][64]f32 = undefined;
    for (0..64) |s_idx| {
        const s = @as(f32, @floatFromInt(s_idx)) * 0.1;
        table[s_idx][0] = 1.0;
        for (1..64) |k| {
            // Restore energy: k^s
            table[s_idx][k] = math.pow(f32, @floatFromInt(k), s);
        }
    }
    break :blk table;
};

pub fn apply_polylog_filter(coeffs: *[64]Complex, s: f32) void {
    const s_idx = @as(usize, @intFromFloat(@min(@max(s * 10.0, 0), 63)));
    const weights = &weight_table[s_idx];
    for (1..64) |k| {
        coeffs[k].re *= weights[k];
        coeffs[k].im *= weights[k];
    }
}

pub fn apply_inverse_polylog_filter(coeffs: *[64]Complex, s: f32) void {
    const s_idx = @as(usize, @intFromFloat(@min(@max(s * 10.0, 0), 63)));
    const weights = &inv_weight_table[s_idx];
    
    // Frequency-Weighted Spectral Masking (FWSM) - Fidelity Max Calibration
    // Lower multiplier (0.0003) preserves micro-textural fur detail and whiskers.
    for (1..64) |k| {
        const freq_factor = @sqrt(@as(f32, @floatFromInt(k)));
        const noise_floor = 0.0001 * freq_factor; 
        const floor_sq = noise_floor * noise_floor;
        
        const mag_sq = coeffs[k].re * coeffs[k].re + coeffs[k].im * coeffs[k].im;
        if (mag_sq > floor_sq) {
            coeffs[k].re *= weights[k];
            coeffs[k].im *= weights[k];

            // Impulse Suppression: prevents extreme spectral peaks from creating "dots"
            // Raised limit to 4.0 to allow stronger transients (fine lines)
            const limit: f32 = 4.0;
            const lim_sq = limit * limit;
            const res_mag_sq = coeffs[k].re * coeffs[k].re + coeffs[k].im * coeffs[k].im;
            if (res_mag_sq > lim_sq) {
                const r = limit / @sqrt(res_mag_sq);
                coeffs[k].re *= r;
                coeffs[k].im *= r;
            }
        } else {
            // Suppress noise at high frequencies
            coeffs[k].re = 0;
            coeffs[k].im = 0;
        }
    }
}

pub fn compress_coeffs(coeffs: *[64]Complex, threshold: f32) usize {
    var kept: usize = 0;
    const t_sq = threshold * threshold;
    for (coeffs) |*c| {
        if (c.re * c.re + c.im * c.im < t_sq) {
            c.re = 0;
            c.im = 0;
        } else {
            kept += 1;
        }
    }
    return kept;
}
