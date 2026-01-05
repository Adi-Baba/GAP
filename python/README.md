# GAP Python Wrapper

A graceful Python interface for the [GAP Image Codec](../../README.md).

## Installation

```bash
cd python
pip install .
```

*Note: You must have the GAP binaries built in `../dist/`.*

## Usage

```python
from pygap import GAP

# 1. Initialize (auto-finds binary in ../dist)
gap = GAP()
# OR specify path manually:
# gap = GAP("path/to/gap.exe")

# 2. Encode (High Fidelity)
gap.encode("input.png", "output.gap", s=0.05, t=0.2)

# 3. Decode
gap.decode("output.gap", "restored.png")
```

## Error Handling

```python
from pygap import GAP, GapError

try:
    gap = GAP()
    gap.decode("broken.gap", "out.png")
except GapError as e:
    print(f"Ouch: {e}")
```
