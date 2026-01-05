const std = @import("std");

const CodeVal = u32;
const Top: CodeVal = 1 << 24;
const Bottom: CodeVal = 1 << 16;
const MaxFreq: CodeVal = 1 << 14;

pub const FrequencyModel = struct {
    counts: [256]u32,
    bit: [257]u32, 
    total: u32,
    
    pub fn init() FrequencyModel {
        var m: FrequencyModel = undefined;
        for (0..256) |i| m.counts[i] = 1;
        m.total = 256;
        m.rebuild_bit();
        return m;
    }
    
    fn rebuild_bit(self: *FrequencyModel) void {
        @as(*[257]u32, @ptrCast(&self.bit)).* = std.mem.zeroes([257]u32);
        for (0..256) |idx| {
            var i: usize = idx + 1;
            const delta = self.counts[idx];
            while (i < 257) : (i += i & -%i) {
                self.bit[i] += delta;
            }
        }
    }

    pub fn update(self: *FrequencyModel, symbol: u8) void {
        var i: usize = @as(usize, symbol) + 1;
        while (i < 257) : (i += i & -%i) {
            self.bit[i] += 1;
        }
        self.counts[symbol] += 1;
        self.total += 1;
        
        if (self.total >= MaxFreq) {
            self.total = 0;
            for (0..256) |j| {
                self.counts[j] = (self.counts[j] >> 1) + 1;
                self.total += self.counts[j];
            }
            self.rebuild_bit();
        }
    }
    
    fn get_cum(self: *const FrequencyModel, symbol: u8) u32 {
        var sum: u32 = 0;
        var i: usize = @as(usize, symbol);
        while (i > 0) : (i -= i & -%i) {
            sum += self.bit[i];
        }
        return sum;
    }

    pub fn get_range(self: *const FrequencyModel, symbol: u8) struct { low: u32, range: u32 } {
        return .{ .low = self.get_cum(symbol), .range = self.counts[symbol] };
    }
    
    pub fn get_symbol(self: *const FrequencyModel, value: u32) struct { symbol: u8, low: u32, range: u32 } {
        var idx: u32 = 0;
        var sum: u32 = 0;
        var probe: u32 = 128; // Largest power of 2 <= 256
        
        while (probe > 0) {
            const next_idx = idx + probe;
            if (next_idx < 257 and sum + self.bit[next_idx] <= value) {
                idx = next_idx;
                sum += self.bit[idx];
            }
            probe >>= 1;
        }
        
        const s = @as(u8, @intCast(@min(idx, 255)));
        return .{ .symbol = s, .low = sum, .range = self.counts[s] };
    }
};

pub const RangeEncoder = struct {
    range: u32,
    low: u64,
    cache: u8,
    ff_count: u32,
    started: bool,
    allocator: std.mem.Allocator,
    buffer: std.ArrayListUnmanaged(u8),
    
    pub fn init(allocator: std.mem.Allocator) RangeEncoder {
        return RangeEncoder{
            .range = 0xFFFFFFFF,
            .low = 0,
            .cache = 0,
            .ff_count = 0,
            .started = false,
            .allocator = allocator,
            .buffer = .{},
        };
    }
    
    pub fn deinit(self: *RangeEncoder) void {
        self.buffer.deinit(self.allocator);
    }
    
    pub fn encode_safe(self: *RangeEncoder, model: *FrequencyModel, symbol: u8) !void {
        const p = model.get_range(symbol);
        const r_div = self.range / model.total;
        self.low +%= @as(u64, p.low) * r_div;
        self.range = p.range * r_div;
        
        while (self.range < (1 << 24)) {
            const byte = @as(u32, @intCast(self.low >> 24));
            try self.write_byte_carry(byte);
            self.low = (self.low << 8) & 0xFFFFFFFF;
            self.range <<= 8;
        }
        model.update(symbol);
    }

    fn write_byte_carry(self: *RangeEncoder, b: u32) !void {
        if (b < 0xFF) {
            if (self.started) {
                try self.buffer.append(self.allocator, self.cache);
                for (0..self.ff_count) |_| try self.buffer.append(self.allocator, 0xFF);
            } else self.started = true;
            self.cache = @intCast(b);
            self.ff_count = 0;
        } else if (b > 0xFF) {
            if (self.started) {
                try self.buffer.append(self.allocator, self.cache + 1);
                for (0..self.ff_count) |_| try self.buffer.append(self.allocator, 0x00);
            } else self.started = true;
            self.cache = @intCast(b & 0xFF);
            self.ff_count = 0;
        } else {
            self.ff_count += 1;
        }
    }

    pub fn finish(self: *RangeEncoder) !void {
        for (0..5) |_| {
            try self.write_byte_carry(@intCast(self.low >> 24));
            self.low = (self.low << 8) & 0xFFFFFFFF;
        }
    }
};

pub const RangeDecoder = struct {
    range: u32,
    code: u32,
    buffer: []const u8,
    cursor: usize,
    
    pub fn init(data: []const u8) RangeDecoder {
        var d = RangeDecoder{
            .range = 0xFFFFFFFF,
            .code = 0,
            .buffer = data,
            .cursor = 0,
        };
        for (0..4) |_| {
            d.code = (d.code << 8) | @as(u32, if (d.cursor < d.buffer.len) d.buffer[d.cursor] else 0);
            if (d.cursor < d.buffer.len) d.cursor += 1;
        }
        return d;
    }
    
    pub fn decode(self: *RangeDecoder, model: *FrequencyModel) u8 {
        const r_div = self.range / model.total;
        const info = model.get_symbol(self.code / r_div);
        
        self.code -= info.low * r_div;
        self.range = info.range * r_div;
        
        while (self.range < (1 << 24)) {
            const next_byte = if (self.cursor < self.buffer.len) self.buffer[self.cursor] else 0;
            if (self.cursor < self.buffer.len) self.cursor += 1;
            self.code = (self.code << 8) | @as(u32, next_byte);
            self.range <<= 8;
        }
        model.update(info.symbol);
        return info.symbol;
    }
};
