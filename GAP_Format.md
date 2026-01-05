# GAP File Format Specification (v1.0)
**Extension:** `.gap`
**Endianness:** Little Endian (LE)

---

## 1. File Structure overview
A `.gap` file consists of a **Global Header** followed by a continuous **Stream of Patches**.

```
[ Magic Bytes (4) ]
[ Global Header (20) ]
[ Patch 0 ]
[ Patch 1 ]
...
[ Patch N ]
```

## 2. Global Header (24 Bytes)

| Offset | Type | Name | Value / Description |
| :--- | :--- | :--- | :--- |
| 0x00 | `[4]u8` | **Magic** | `0x47 0x41 0x50 0x01` ("GAP" + Ver 1) |
| 0x04 | `u32` | **Width** | Image Width in pixels |
| 0x08 | `u32` | **Height** | Image Height in pixels |
| 0x0C | `f32` | **S-Value** | PLTM Decay Parameter (e.g. 0.1) |
| 0x10 | `f32` | **Threshold** | Coefficient Cutoff (e.g. 0.5) |
| 0x14 | `u32` | **Flags** | Reserved (0) |

## 3. Patch Data
The image is split into **8x8** blocks.
*   **Order:** Raster Scan (Left->Right, Top->Bottom).
*   **Total Patches:** `ceil(Width/8) * ceil(Height/8)`.

Each Patch is variable length:

| Component | Type | Size | Description |
| :--- | :--- | :--- | :--- |
| **Angle** | `u8` | 1 Byte | Quantized Gradient Angle $\theta$.<br>Map: $0 \to 0$, $255 \to 2\pi$. |
| **Count** | `u8` | 1 Byte | Number of coefficients kept ($K$).<br>Range: $0 \dots 64$. |
| **Coeffs** | `[K]f32` | $4 \times K$ Bytes | The PLTM Spectral Coefficients.<br>(Currently storing Magnitude/Real only for simplicity, or interleaved Complex if full reconstruction needed). |

### 3.1 Coefficient Optimization
To start, we store coefficients as **Raw Floats (f32)**.
*   **Size per Patch:** $2 + (4 \times K)$ bytes.
*   **Worst Case (Noise):** $K=64 \to 258$ bytes/patch. (Matches raw 8x8 floats).
*   **Best Case (Flat):** $K=1 \to 6$ bytes/patch. (**42x Compression**).

## 4. Example Layout
**16x8 Image (2 Patches)**

```hex
47 41 50 01   ; Magic "GAP1"
10 00 00 00   ; Width 16
08 00 00 00   ; Height 8
CD CC CC 3D   ; S = 0.1
00 00 00 3F   ; Threshold = 0.5
00 00 00 00   ; Flags = 0

; Patch 0 (Flat Color)
00            ; Angle = 0
01            ; Count = 1 (DC only)
00 00 80 3F   ; Val = 1.0

; Patch 1 (Complex Texture)
80            ; Angle = Pi (180 deg)
04            ; Count = 4
... (16 bytes of floats) ...
```

## 5. Implementation Notes
*   **Padding:** If Width/Height are not multiples of 8, the encoder must pad the input image to the nearest 8x8 boundary. The `Width`/`Height` in the header are the *original* dimensions, used for cropping during decode.
*   **Quantization:** Angle is quantized to `angle / (2*PI) * 255`.
