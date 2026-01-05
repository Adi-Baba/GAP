import os
import subprocess
import shutil
import platform
import urllib.request
import sys
from pathlib import Path

class GapError(Exception):
    """Raised when the GAP engine fails to encode or decode."""
    pass

class GAP:
    """
    A graceful wrapper for the GAP Image Codec.
    """
    def __init__(self, binary_path=None):
        self.binary_path = binary_path or self._find_binary()
        if not self.binary_path or not os.path.exists(self.binary_path):
            raise FileNotFoundError(f"GAP binary not found. Please provide a path or ensure it's in the dist folder. Searched: {self.binary_path}")

    def _find_binary(self):
        """Auto-detects the GAP binary based on the OS."""
        system = platform.system().lower()
        machine = platform.machine().lower()

        # Normalization
        if machine == "x86_64" or machine == "amd64":
            arch = "amd64"
        elif "arm" in machine or "aarch64" in machine:
            arch = "arm64"
        else:
            arch = "amd64" # Default fallback for now

        if system == "windows":
            ext = ".exe"
            os_name = "windows"
        elif system == "darwin":
            ext = ""
            os_name = "darwin"
        else:
             ext = ""
             os_name = "linux"

        # 1. Look in bundled bin location relative to this file
        # python/pygap/bin/{os-arch}/gap[.exe]
        base_dir = Path(__file__).resolve().parent
        bundled_path = base_dir / "bin" / f"{os_name}-{arch}" / f"gap{ext}"
        
        if bundled_path.exists():
            return str(bundled_path)

        # 2. Look in standard dist location (dev environment)
        dist_path = base_dir.parent.parent.parent / "dist" / f"{os_name}-{arch}" / f"gap{ext}"
        if dist_path.exists():
            return str(dist_path)
            
        # 2. Look in system PATH
        binary_name = f"gap{ext}"
        path_binary = shutil.which(binary_name)
        if path_binary:
            return path_binary
            
        # 3. Auto-download if missing
        return self._download_binary(os_name, arch, ext)

    def _download_binary(self, os_name, arch, ext):
        """Downloads the binary from GitHub Releases to a local cache."""
        binary_name = f"gap{ext}"
        # Naming convention for release assets: gap-windows-amd64.exe, gap-linux-amd64, etc.
        release_asset_name = f"gap-{os_name}-{arch}{ext}"
        
        # Cache directory: ~/.gap/bin/
        cache_dir = Path.home() / ".gap" / "bin"
        cache_dir.mkdir(parents=True, exist_ok=True)
        local_path = cache_dir / binary_name
        
        if local_path.exists():
            return str(local_path)
            
        url = f"https://github.com/Adi-Baba/GAP/releases/latest/download/{release_asset_name}"
        print(f"[GAP] Binary not found. Downloading from: {url}")
        print(f"[GAP] Target: {local_path}")
        
        try:
            with urllib.request.urlopen(url) as response, open(local_path, 'wb') as out_file:
                shutil.copyfileobj(response, out_file)
        except Exception as e:
            # Clean up partial download
            if local_path.exists():
                local_path.unlink()
            raise GapError(f"Failed to download binaries. Please install manually or check connection. Error: {e}")
            
        print("[GAP] Download complete.")
        
        # Make executable on Unix
        if os_name != "windows":
            current_mode = os.stat(local_path).st_mode
            os.chmod(local_path, current_mode | 0o111)
            
        return str(local_path)

    def encode(self, input_path: str, output_path: str, s: float = 0.1, t: float = 0.5) -> None:
        """
        Compress an image into a .gap file.
        
        Args:
            input_path: Path to the source image (PNG/JPG).
            output_path: Path to the destination .gap file.
            s (float): Spectral Sensitivity. Lower = Higher Quality. Default 0.1. (Rec: 0.05 for HQ).
            t (float): Threshold. Lower = Less Compression. Default 0.5. (Rec: 0.2 for HQ).
            
        Raises:
            GapError: If the process fails.
        """
        if not os.path.exists(input_path):
             raise FileNotFoundError(f"Input file not found: {input_path}")
             
        cmd = [
            self.binary_path, "encode",
            "-i", str(input_path),
            "-o", str(output_path),
            "-s", str(s),
            "-t", str(t)
        ]
        
        result = subprocess.run(cmd, capture_output=True, text=True)
        if result.returncode != 0:
            raise GapError(f"Encoding failed: {result.stdout} {result.stderr}")

    def decode(self, input_path: str, output_path: str) -> None:
        """
        Decompress a .gap file to an image.
        
        Args:
            input_path: Path to the source .gap file.
            output_path: Path to the destination image (PNG).
            
        Raises:
            GapError: If the process fails.
        """
        if not os.path.exists(input_path):
             raise FileNotFoundError(f"Input file not found: {input_path}")
             
        cmd = [
            self.binary_path, "decode",
            "-i", str(input_path),
            "-o", str(output_path)
        ]
        
        result = subprocess.run(cmd, capture_output=True, text=True)
        if result.returncode != 0:
            raise GapError(f"Decoding failed: {result.stdout} {result.stderr}")
