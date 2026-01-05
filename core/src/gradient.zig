const std = @import("std");
const math = std.math;

pub const PatchSize = 8;
pub const PixelCount = PatchSize * PatchSize;

pub const GradientResult = struct {
    dx: f32,
    dy: f32,
    magnitude: f32,
    angle: f32,
};

/// Computes the dominant gradient vector for an 8x8 patch.
/// Input: Flat array of 64 floats (row-major).
pub fn compute_gradient(patch: *const [PixelCount]f32) GradientResult {
    var sum_dx: f32 = 0;
    var sum_dy: f32 = 0;

    // Simple central difference (skip borders for simplicity or handle clamp)
    // We'll just loop y: 0..7, x: 0..7 and use forward diffs for boundary safety
    // or standard Sobel. Let's use simple differences for speed on 8x8.
    
    for (0..8) |y| {
        for (0..7) |x| {
            sum_dx += patch[y * 8 + x + 1] - patch[y * 8 + x];
        }
    }
    for (0..7) |y| {
        for (0..8) |x| {
            sum_dy += patch[(y + 1) * 8 + x] - patch[y * 8 + x];
        }
    }

    // Average diff
    // The number of horizontal distinct pairs is H * (W-1) = 8*7 = 56
    const count: f32 = 56.0; 
    const avg_dx = sum_dx / count;
    const avg_dy = sum_dy / count;
    
    const mag = @sqrt(avg_dx * avg_dx + avg_dy * avg_dy);
    const ang = math.atan2(avg_dy, avg_dx);

    return GradientResult{
        .dx = avg_dx,
        .dy = avg_dy,
        .magnitude = mag,
        .angle = ang,
    };
}

const angle_map_table: [256][PixelCount]u8 = blk: {
    @setEvalBranchQuota(1000000);
    var table: [256][PixelCount]u8 = undefined;
    
    // Internal Entry for sorting during precomputation
    const Entry = struct {
        proj: f32,
        index: u8,
        pub fn lessThan(context: void, a: @This(), b: @This()) bool {
            _ = context;
            if (a.proj != b.proj) {
                return a.proj < b.proj;
            }
            // Stable sort tie-breaker: use original index
            return a.index < b.index;
        }
    };
    
    for (0..256) |i| {
        const angle = @as(f32, @floatFromInt(i)) / 255.0 * 2.0 * math.pi;
        const cos_a = @cos(angle);
        const sin_a = @sin(angle);
        
        var entries: [PixelCount]Entry = undefined;
        for (0..PatchSize) |y| {
            for (0..PatchSize) |x| {
                const idx = y * PatchSize + x;
                entries[idx] = Entry{
                    .proj = @as(f32, @floatFromInt(x)) * cos_a + @as(f32, @floatFromInt(y)) * sin_a,
                    .index = @intCast(idx),
                };
            }
        }
        
        std.sort.block(Entry, &entries, {}, Entry.lessThan);
        
        for (entries, 0..) |entry, j| {
            table[i][j] = entry.index;
        }
    }
    break :blk table;
};

/// Returns a pointer to the precomputed sorting indices for a given angle.
pub fn get_gradient_map_ptr(angle: f32) *const [PixelCount]u8 {
    var norm_angle = angle;
    const two_pi = 2.0 * math.pi;
    while (norm_angle < 0) : (norm_angle += two_pi) {}
    while (norm_angle >= two_pi) : (norm_angle -= two_pi) {}

    const factor = 255.0 / two_pi;
    const fIdx = norm_angle * factor; // Truncation
    
    const idx: usize = @intFromFloat(@max(0.0, fIdx));
    const clamped_idx = @min(idx, 255);
    return &angle_map_table[clamped_idx];
}

/// Generates the sorting indices based on the gradient angle index (0..255).
pub fn get_gradient_map(angle: f32, out_indices: *[PixelCount]u8) void {
    @memcpy(out_indices, get_gradient_map_ptr(angle));
}
