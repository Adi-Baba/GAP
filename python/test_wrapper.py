import sys
import os
from pygap import GAP, GapError

def test_wrapper():
    print("Testing GAP Python Wrapper...")
    
    # 1. Initialize
    try:
        gap = GAP()
        print(f"[OK] Found binary at: {gap.binary_path}")
    except Exception as e:
        print(f"[FAIL] Init: {e}")
        return

    # 2. Setup paths
    # Assuming run from d:\ISHNeuron\GAP directory for convenient relative paths, 
    # but the script is in d:\ISHNeuron\GAP\python
    base_dir = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
    input_png = os.path.join(base_dir, "decoded", "Parrot.png")
    output_gap = os.path.join(base_dir, "dist", "test_wrapper.gap")
    output_png = os.path.join(base_dir, "dist", "test_wrapper.png")

    if not os.path.exists(input_png):
        print(f"[SKIP] Input not found: {input_png}")
        return

    # 3. Encode
    print("Encoding...", end=" ")
    try:
        gap.encode(input_png, output_gap, s=0.1, t=0.5)
        if os.path.exists(output_gap):
            print("[OK]")
        else:
            print("[FAIL] Output file not created")
    except GapError as e:
        print(f"[FAIL] {e}")

    # 4. Decode
    print("Decoding...", end=" ")
    try:
        gap.decode(output_gap, output_png)
        if os.path.exists(output_png):
            print("[OK]")
        else:
            print("[FAIL] Output png not created")
    except GapError as e:
        print(f"[FAIL] {e}")

    print("Done.")

if __name__ == "__main__":
    test_wrapper()
