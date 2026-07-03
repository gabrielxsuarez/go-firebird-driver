#!/usr/bin/env python3
"""
Profiling and benchmarking tool for go-firebird-driver.

Checks that Firebird containers are running (via docker or podman),
runs Go benchmarks with CPU/memory profiling for each driver component,
and generates a structured report.

Usage:
    python scripts/profile.py                    # full run
    python scripts/profile.py --compare <dir>    # compare with previous run
    python scripts/profile.py --wire-only        # wire benchmarks only (no server)
    python scripts/profile.py --count 10         # 10 iterations per benchmark
"""

import argparse
import datetime
import os
import platform
import shutil
import socket
import subprocess
import sys
import time
from pathlib import Path

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

DRIVER_ROOT = Path(__file__).resolve().parent.parent
COMPOSE_FILE = DRIVER_ROOT / "docker" / "firebird" / "docker-compose.yml"
PROFILES_DIR = DRIVER_ROOT / "profiles"

PORTS = {"FB3": 3063, "FB4": 3064, "FB5": 3065}
REQUIRED_PORT = PORTS["FB3"]  # minimum for driver benchmarks

BENCH_CATEGORIES = [
    {
        "name": "XDR Encoding/Decoding",
        "package": "./internal/wire/...",
        "pattern": "^(BenchmarkRead|BenchmarkWrite)",
        "needs_server": False,
        "prof_prefix": "xdr",
    },
    {
        "name": "Encryption",
        "package": "./internal/wire/...",
        "pattern": "^(BenchmarkArc4|BenchmarkChaCha|BenchmarkCrypt)",
        "needs_server": False,
        "prof_prefix": "crypt",
    },
    {
        "name": "Parameter Encoding",
        "package": "./internal/wire/...",
        "pattern": "^(BenchmarkEncodeParams|BenchmarkEstimate|BenchmarkStackWriter|BenchmarkToString|BenchmarkRepeatZeros|BenchmarkScaledInt)",
        "needs_server": False,
        "prof_prefix": "params",
    },
    {
        "name": "Driver Operations",
        "package": ".",
        "pattern": "^Benchmark",
        "needs_server": True,
        "prof_prefix": "driver",
    },
]

# ---------------------------------------------------------------------------
# Utilities
# ---------------------------------------------------------------------------

def log(msg: str) -> None:
    print(f"  {msg}")


def header(msg: str) -> None:
    print(f"\n{'='*60}")
    print(f"  {msg}")
    print(f"{'='*60}")


def check_port(host: str, port: int, timeout: float = 1.0) -> bool:
    """Return True if a TCP port is accepting connections."""
    try:
        with socket.create_connection((host, port), timeout=timeout):
            return True
    except (OSError, ConnectionRefusedError):
        return False


def find_runtime() -> str | None:
    """Find docker or podman, return the command name or None."""
    for cmd in ("docker", "podman"):
        if shutil.which(cmd):
            try:
                r = subprocess.run(
                    [cmd, "info"],
                    capture_output=True, timeout=10,
                )
                if r.returncode == 0:
                    return cmd
            except (subprocess.TimeoutExpired, FileNotFoundError):
                continue
    return None


def compose_cmd(runtime: str) -> list[str]:
    """Return the compose sub-command for the given runtime."""
    if runtime == "podman":
        if shutil.which("podman-compose"):
            return ["podman-compose"]
        return ["podman", "compose"]
    return ["docker", "compose"]


def start_containers(runtime: str) -> bool:
    """Start Firebird containers via compose. Returns True on success."""
    cmd = compose_cmd(runtime) + ["-f", str(COMPOSE_FILE), "up", "-d"]
    log(f"Starting containers: {' '.join(cmd)}")
    r = subprocess.run(cmd, capture_output=True, text=True, timeout=120)
    if r.returncode != 0:
        log(f"ERROR: {r.stderr.strip()}")
        return False
    return True


def wait_for_port(host: str, port: int, timeout: int = 30) -> bool:
    """Wait until a port is accepting connections."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        if check_port(host, port):
            return True
        time.sleep(1)
    return False


def go_version() -> str:
    r = subprocess.run(["go", "version"], capture_output=True, text=True)
    return r.stdout.strip() if r.returncode == 0 else "unknown"


def cpu_info() -> str:
    if platform.system() == "Windows":
        return os.environ.get("PROCESSOR_IDENTIFIER", platform.processor())
    try:
        with open("/proc/cpuinfo") as f:
            for line in f:
                if "model name" in line:
                    return line.split(":")[1].strip()
    except FileNotFoundError:
        pass
    return platform.processor()


# ---------------------------------------------------------------------------
# Benchmark runners
# ---------------------------------------------------------------------------

def run_benchmarks(
    category: dict,
    output_dir: Path,
    count: int,
    timeout: int = 300,
) -> tuple[bool, str]:
    """
    Run benchmarks for a category.
    Returns (success, output_text).
    """
    prefix = category["prof_prefix"]
    bench_file = output_dir / f"{prefix}_bench.txt"
    cpu_prof = output_dir / f"{prefix}_cpu.prof"
    mem_prof = output_dir / f"{prefix}_mem.prof"

    cmd = [
        "go", "test",
        "-run=^$",
        f"-bench={category['pattern']}",
        "-benchmem",
        f"-count={count}",
        f"-cpuprofile={cpu_prof}",
        f"-memprofile={mem_prof}",
        f"-timeout={timeout}s",
        category["package"],
    ]

    log(f"Running: {' '.join(cmd)}")
    r = subprocess.run(
        cmd,
        capture_output=True,
        text=True,
        cwd=str(DRIVER_ROOT),
        timeout=timeout + 30,
    )

    output = r.stdout + r.stderr
    bench_file.write_text(output, encoding="utf-8")

    if r.returncode != 0 and "no tests to run" not in output:
        log(f"WARN: exit code {r.returncode}")

    return r.returncode == 0, output


def run_benchstat(old: Path, new: Path, output_dir: Path) -> str | None:
    """Run benchstat comparison between two result files. Returns output or None."""
    if not shutil.which("benchstat"):
        try:
            subprocess.run(
                ["go", "install", "golang.org/x/perf/cmd/benchstat@latest"],
                capture_output=True, timeout=60,
            )
        except (subprocess.TimeoutExpired, FileNotFoundError):
            return None

    if not shutil.which("benchstat"):
        return None

    r = subprocess.run(
        ["benchstat", str(old), str(new)],
        capture_output=True, text=True, timeout=30,
    )
    return r.stdout if r.returncode == 0 else None


def generate_pprof_text(prof_file: Path, output_file: Path) -> bool:
    """Generate a text summary from a .prof file using go tool pprof."""
    if not prof_file.exists() or prof_file.stat().st_size == 0:
        return False
    r = subprocess.run(
        ["go", "tool", "pprof", "-text", "-nodecount=30", str(prof_file)],
        capture_output=True, text=True, timeout=30,
        cwd=str(DRIVER_ROOT),
    )
    if r.returncode == 0 and r.stdout.strip():
        output_file.write_text(r.stdout, encoding="utf-8")
        return True
    return False


# ---------------------------------------------------------------------------
# Report generation
# ---------------------------------------------------------------------------

def extract_bench_lines(text: str) -> list[str]:
    """Extract only Benchmark result lines from go test output."""
    lines = []
    for line in text.splitlines():
        stripped = line.strip()
        if stripped.startswith("Benchmark") and ("ns/op" in stripped or "B/op" in stripped):
            lines.append(stripped)
    return lines


def generate_report(
    output_dir: Path,
    results: dict[str, tuple[bool, str]],
    compare_dir: Path | None,
    count: int,
) -> str:
    """Generate a Markdown report summarizing all benchmark results."""
    now = datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    lines = [
        f"# Profiling Report — go-firebird-driver",
        f"",
        f"**Date:** {now}  ",
        f"**Go:** {go_version()}  ",
        f"**OS:** {platform.system()} {platform.release()} ({platform.machine()})  ",
        f"**CPU:** {cpu_info()}  ",
        f"**Iterations:** {count}  ",
        f"",
    ]

    # Port status
    lines.append("## Environment")
    lines.append("")
    for name, port in PORTS.items():
        status = "✅ up" if check_port("localhost", port) else "❌ down"
        lines.append(f"- {name} (port {port}): {status}")
    lines.append("")

    # Benchmark results per category
    for cat in BENCH_CATEGORIES:
        name = cat["name"]
        prefix = cat["prof_prefix"]
        lines.append(f"## {name}")
        lines.append("")

        if name in results:
            success, output = results[name]
            bench_lines = extract_bench_lines(output)
            if bench_lines:
                lines.append("```")
                for bl in bench_lines:
                    lines.append(bl)
                lines.append("```")
            else:
                lines.append("*No benchmark results found.*")
        else:
            lines.append("*Skipped (server not available).*")
        lines.append("")

        # CPU profile summary
        cpu_text = output_dir / f"{prefix}_cpu_top.txt"
        if cpu_text.exists():
            lines.append(f"### CPU Profile (top 30)")
            lines.append("")
            lines.append("```")
            lines.append(cpu_text.read_text(encoding="utf-8").strip())
            lines.append("```")
            lines.append("")

        # Memory profile summary
        mem_text = output_dir / f"{prefix}_mem_top.txt"
        if mem_text.exists():
            lines.append(f"### Memory Profile (top 30)")
            lines.append("")
            lines.append("```")
            lines.append(mem_text.read_text(encoding="utf-8").strip())
            lines.append("```")
            lines.append("")

    # Benchstat comparison
    if compare_dir:
        lines.append("## Comparison with Previous Run")
        lines.append("")
        lines.append(f"Compared against: `{compare_dir}`")
        lines.append("")
        has_comparison = False
        for cat in BENCH_CATEGORIES:
            prefix = cat["prof_prefix"]
            old_bench = compare_dir / f"{prefix}_bench.txt"
            new_bench = output_dir / f"{prefix}_bench.txt"
            if old_bench.exists() and new_bench.exists():
                stat = run_benchstat(old_bench, new_bench, output_dir)
                if stat:
                    has_comparison = True
                    lines.append(f"### {cat['name']}")
                    lines.append("")
                    lines.append("```")
                    lines.append(stat.strip())
                    lines.append("```")
                    lines.append("")
        if not has_comparison:
            lines.append("*No comparable benchmark files found.*")
            lines.append("")

    # Files list
    lines.append("## Output Files")
    lines.append("")
    for f in sorted(output_dir.iterdir()):
        size = f.stat().st_size
        if size > 1024:
            size_str = f"{size/1024:.1f} KB"
        else:
            size_str = f"{size} B"
        lines.append(f"- `{f.name}` ({size_str})")
    lines.append("")

    return "\n".join(lines)


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main() -> int:
    parser = argparse.ArgumentParser(
        description="Profile and benchmark go-firebird-driver",
    )
    parser.add_argument(
        "--compare", type=str, default=None,
        help="Path to a previous profile directory for benchstat comparison",
    )
    parser.add_argument(
        "--wire-only", action="store_true",
        help="Only run wire package benchmarks (no server required)",
    )
    parser.add_argument(
        "--count", type=int, default=5,
        help="Number of benchmark iterations (default: 5)",
    )
    parser.add_argument(
        "--timeout", type=int, default=300,
        help="Timeout per benchmark category in seconds (default: 300)",
    )
    args = parser.parse_args()

    header("go-firebird-driver Profiling Tool")
    log(f"Driver root: {DRIVER_ROOT}")
    log(f"Go version:  {go_version()}")
    log(f"Platform:    {platform.system()} {platform.machine()}")
    log(f"Count:       {args.count}")

    # Verify go is available
    if not shutil.which("go"):
        log("ERROR: 'go' not found in PATH")
        return 1

    # -----------------------------------------------------------------------
    # 1. Check / start Firebird containers
    # -----------------------------------------------------------------------
    server_available = False

    if not args.wire_only:
        header("Checking Firebird Containers")

        ports_up = {name: check_port("localhost", port) for name, port in PORTS.items()}
        for name, up in ports_up.items():
            status = "✅ up" if up else "❌ down"
            log(f"{name} (port {PORTS[name]}): {status}")

        if ports_up["FB3"]:
            server_available = True
            log("FB3 is ready — proceeding with all benchmarks")
        else:
            log("FB3 not available — attempting to start containers...")

            runtime = find_runtime()
            if runtime is None:
                log("ERROR: Neither docker nor podman is available/running.")
                log("Please start Firebird containers manually, or use --wire-only.")
                log("")
                log("To start manually:")
                log(f"  docker compose -f {COMPOSE_FILE} up -d")
                log("")
                log("Falling back to wire-only benchmarks.")
                args.wire_only = True
            else:
                log(f"Found container runtime: {runtime}")
                if not start_containers(runtime):
                    log("ERROR: Failed to start containers.")
                    log("Falling back to wire-only benchmarks.")
                    args.wire_only = True
                else:
                    log("Waiting for FB3 to accept connections...")
                    if wait_for_port("localhost", REQUIRED_PORT, timeout=45):
                        server_available = True
                        log("FB3 is ready ✅")
                    else:
                        log("ERROR: FB3 did not become available within 45s.")
                        log("Falling back to wire-only benchmarks.")
                        args.wire_only = True

    # -----------------------------------------------------------------------
    # 2. Create output directory
    # -----------------------------------------------------------------------
    timestamp = datetime.datetime.now().strftime("%Y%m%d_%H%M%S")
    output_dir = PROFILES_DIR / timestamp
    output_dir.mkdir(parents=True, exist_ok=True)
    log(f"Output directory: {output_dir}")

    # Create/update 'latest' symlink or marker
    latest_marker = PROFILES_DIR / "latest.txt"
    latest_marker.write_text(str(output_dir), encoding="utf-8")

    # -----------------------------------------------------------------------
    # 3. Run benchmarks per category
    # -----------------------------------------------------------------------
    results: dict[str, tuple[bool, str]] = {}

    for cat in BENCH_CATEGORIES:
        name = cat["name"]
        if cat["needs_server"] and not server_available:
            log(f"\nSkipping '{name}' (server not available)")
            continue

        header(f"Benchmarking: {name}")
        success, output = run_benchmarks(cat, output_dir, args.count, args.timeout)
        results[name] = (success, output)

        bench_lines = extract_bench_lines(output)
        if bench_lines:
            log(f"  {len(bench_lines)} benchmark results captured")
        else:
            log("  No benchmark results")

        # Generate pprof text summaries
        prefix = cat["prof_prefix"]
        cpu_prof = output_dir / f"{prefix}_cpu.prof"
        mem_prof = output_dir / f"{prefix}_mem.prof"
        if generate_pprof_text(cpu_prof, output_dir / f"{prefix}_cpu_top.txt"):
            log("  CPU profile → text summary generated")
        if generate_pprof_text(mem_prof, output_dir / f"{prefix}_mem_top.txt"):
            log("  Memory profile → text summary generated")

    # -----------------------------------------------------------------------
    # 4. Generate report
    # -----------------------------------------------------------------------
    header("Generating Report")

    compare_dir = Path(args.compare) if args.compare else None
    report = generate_report(output_dir, results, compare_dir, args.count)

    report_file = output_dir / "report.md"
    report_file.write_text(report, encoding="utf-8")
    log(f"Report saved to: {report_file}")

    # Also save a combined benchmark file for easy benchstat use
    combined = output_dir / "all_bench.txt"
    all_lines = []
    for cat in BENCH_CATEGORIES:
        name = cat["name"]
        if name in results:
            _, output = results[name]
            all_lines.append(f"# {name}")
            all_lines.extend(extract_bench_lines(output))
            all_lines.append("")
    combined.write_text("\n".join(all_lines), encoding="utf-8")

    # -----------------------------------------------------------------------
    # 5. Summary
    # -----------------------------------------------------------------------
    header("Summary")

    total_benches = sum(
        len(extract_bench_lines(out)) for _, (_, out) in results.items()
    )
    log(f"Categories run:      {len(results)}/{len(BENCH_CATEGORIES)}")
    log(f"Total benchmarks:    {total_benches}")
    log(f"Output directory:    {output_dir}")
    log(f"Report:              {report_file}")
    if compare_dir:
        log(f"Compared against:    {compare_dir}")
    log("")
    log("To compare with a future run:")
    log(f"  python scripts/profile.py --compare {output_dir}")
    log("")
    log("To inspect CPU profiles interactively:")
    for cat in BENCH_CATEGORIES:
        prefix = cat["prof_prefix"]
        prof = output_dir / f"{prefix}_cpu.prof"
        if prof.exists():
            log(f"  go tool pprof -http=:8080 {prof}")

    return 0


if __name__ == "__main__":
    sys.exit(main())
