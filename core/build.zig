const std = @import("std");

pub fn build(b: *std.Build) void {
    const target = b.standardTargetOptions(.{});
    const optimize = b.standardOptimizeOption(.{});

    // Zig 0.16+: Use createModule + addLibrary
    const mod = b.createModule(.{
        .root_source_file = b.path("src/root.zig"),
        .target = target,
        .optimize = optimize,
    });

    const lib = b.addLibrary(.{
        .name = "gap",
        .root_module = mod,
        .linkage = .static,
    });
    b.installArtifact(lib);

    const shared = b.addLibrary(.{
        .name = "gap",
        .root_module = mod,
        .linkage = .dynamic,
    });
    b.installArtifact(shared);
}
