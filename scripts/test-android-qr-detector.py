#!/usr/bin/env python3
"""Exercise the pinned native QR core using only tracked disposable fixtures.

This is an optical regression test, not Android JNI or physical-camera acceptance.
No images, decoded text, customer inputs or network requests are produced.
"""

import argparse
from importlib.metadata import PackageNotFoundError, version
import json
from pathlib import Path
import statistics
import sys
import time


ENGINE_VERSION = "3.1.1"
ROOT = Path(__file__).resolve().parent.parent
GRID = ROOT / "android/app/src/test/resources/qr/direct-v2-upright.txt"
WIRE = ROOT / "testdata/direct-v2-wire.json"


def dependencies():
    try:
        installed = version("zxing-cpp")
        if installed != ENGINE_VERSION:
            raise RuntimeError(f"Expected zxing-cpp=={ENGINE_VERSION}; found {installed}.")
        import zxingcpp
        from PIL import Image, ImageFilter
    except (ImportError, PackageNotFoundError, RuntimeError) as error:
        raise RuntimeError(
            f"{error}\nInstall test dependencies in an isolated Python environment: "
            f"python -m pip install zxing-cpp=={ENGINE_VERSION} Pillow\n"
            "Activate that environment and rerun this script; nothing is installed automatically."
        ) from None
    return zxingcpp, Image, ImageFilter


def fixture(Image):
    rows = GRID.read_text(encoding="ascii").splitlines()
    if not 29 <= len(rows) <= 185 or any(
        len(row) != len(rows) or set(row) - {"0", "1"} for row in rows
    ):
        raise ValueError("The tracked disposable QR grid must be square binary modules.")
    expected = json.loads(WIRE.read_text(encoding="utf-8"))["invitation"].encode("utf-8")
    modules = Image.frombytes(
        "L", (len(rows), len(rows)), bytes(0 if bit == "1" else 255 for row in rows for bit in row)
    )
    # First reconstruct the real four-pixel-per-module desktop rendering.
    # Fractional camera resampling must operate on that rendering, not a pure-symbol shortcut.
    return modules.resize((len(rows) * 4, len(rows) * 4), Image.Resampling.NEAREST), expected


def cases(qr, Image, ImageFilter):
    def sensor(size, angle=0, offset=None, blur=0, dimensions=(1280, 960)):
        symbol = qr.rotate(angle).resize((size, size), Image.Resampling.BILINEAR)
        if blur:
            symbol = symbol.filter(ImageFilter.GaussianBlur(blur))
        frame = Image.new("L", dimensions, 90)
        point = offset or ((dimensions[0] - size) // 2, (dimensions[1] - size) // 2)
        frame.paste(symbol, point)
        return frame

    # Match the dependency-free historical Java regression's floor-indexed
    # module rendering exactly; Java's upright/90-degree reader misses this case.
    modules = qr.resize((qr.width // 4, qr.height // 4), Image.Resampling.NEAREST)
    pixels = modules.tobytes()
    symbol = Image.frombytes("L", (480, 480), bytes(
        pixels[(y * modules.height // 480) * modules.width + x * modules.width // 480]
        for y in range(480) for x in range(480)
    ))
    historical = Image.new("L", (1280, 960), 90)
    historical.paste(symbol, (400, 240))
    yield "historical-java-miss-1280x960-qr480-nearest", historical, True

    for size in (384, 480, 576, 672, 768, 864):
        for angle in (0, 90, 180, 270):
            yield f"sensor-1280x960-qr{size}-r{angle}", sensor(size, angle), True
    for offset in ((24, 24), (680, 24), (24, 360), (680, 360)):
        for angle in (0, 90):
            yield f"offset-{offset[0]}-{offset[1]}-r{angle}", sensor(576, angle, offset), True
    for size in (463, 617):
        for angle in (0, 90, 180, 270):
            yield f"fractional-qr{size}-r{angle}", sensor(size, angle, blur=0.35), True
    for angle in (0, 90, 180, 270):
        yield f"padded-row-1280x960-r{angle}", sensor(576, angle), True
    # Below two pixels/module, lost optical detail is expected. Record these
    # results without turning an undecodable low-resolution frame into a failure.
    for size in (192, 240, 288):
        yield f"low-resolution-640x480-qr{size}", sensor(size, dimensions=(640, 480)), False


def main(argv=None):
    parser = argparse.ArgumentParser(
        description=__doc__,
        epilog=(
            "Requires Python, Pillow and zxing-cpp==3.1.1 (no automatic downloads). "
            "Use an isolated Python environment. Inputs are fixed tracked public fixtures; "
            "there is intentionally no customer-image argument."
        ),
    )
    parser.add_argument("--verbose", action="store_true", help="Print sanitized per-frame outcomes.")
    parser.add_argument("--json-report", type=Path, help="Write sanitized results to a new JSON file.")
    args = parser.parse_args(argv)
    try:
        engine, Image, ImageFilter = dependencies()
        qr, expected = fixture(Image)
        results = []
        for name, frame, required in cases(qr, Image, ImageFilter):
            image = frame
            if name.startswith("padded-row-"):
                width, height = frame.size
                stride = width + 17
                pixels = frame.tobytes()
                # Keep storage alive throughout the native call. Padding has a
                # distinct shade and must not become part of the image columns.
                storage = bytearray([23]) * (stride * height)
                for row in range(height):
                    storage[row * stride:row * stride + width] = pixels[row * width:(row + 1) * width]
                image = engine.ImageView(memoryview(storage), width, height, engine.ImageFormat.Lum, stride, 1)
            started = time.perf_counter()
            # Match Android's QR-only, rotating/downscaling, non-inverting reader.
            # read_barcode limits results to one; pure mode is deliberately disabled.
            code = engine.read_barcode(
                image, formats=engine.BarcodeFormat.QRCode,
                try_rotate=True, try_downscale=True, try_invert=False,
                text_mode=engine.TextMode.Plain, binarizer=engine.Binarizer.LocalAverage,
                is_pure=False, return_errors=False,
            )
            outcome = "none" if code is None else "exact" if code.bytes == expected else "mismatch"
            result = {
                "name": name, "required": required, "outcome": outcome,
                "milliseconds": round((time.perf_counter() - started) * 1000, 2),
            }
            results.append(result)
            if args.verbose:
                print(json.dumps(result, separators=(",", ":")))
        required = [item for item in results if item["required"]]
        failed = [item for item in results if item["outcome"] == "mismatch" or (
            item["required"] and item["outcome"] != "exact"
        )]
        summary = {
            "engine": f"zxing-cpp=={ENGINE_VERSION}", "pillow": version("Pillow"),
            "required": len(required), "requiredPassed": sum(item["outcome"] == "exact" for item in required),
            "lowResolutionObservations": len(results) - len(required),
            "failures": len(failed),
            "medianMilliseconds": round(statistics.median(item["milliseconds"] for item in results), 2),
            "scope": "Native core only; Android JNI and physical camera acceptance remain separate.",
        }
        if args.json_report:
            # Exclusive creation preserves earlier validation evidence.
            with args.json_report.open("x", encoding="utf-8") as report:
                json.dump({"summary": summary, "frames": results}, report, indent=2)
                report.write("\n")
        print(json.dumps(summary, indent=2))
        for item in failed:
            print(f"FAIL: {item['name']} ({item['outcome']})", file=sys.stderr)
        return 1 if failed else 0
    except (OSError, ValueError, RuntimeError) as error:
        print(f"QR detector check failed: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
