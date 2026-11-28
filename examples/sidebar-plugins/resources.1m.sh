#!/bin/sh
# ikesu.title = マシン
# Shows real CPU usage (0-100% via mach host_statistics), memory usage, disk
# usage, and load average (1m/5m/15m) on macOS, styled with ANSI bars.
exec /usr/bin/python3 -I - <<'PY'
import ctypes
import json
import os
import subprocess
import tempfile
import time
import unicodedata

COLS = max(16, int(os.environ.get("IKESU_SIDEBAR_COLUMNS") or 24))

OFF = "\033[0m"
C_CPU = "\033[38;2;224;160;139m"   # peach
C_MEM = "\033[38;2;137;180;250m"   # blue
C_DISK = "\033[38;2;203;166;247m"  # mauve
C_LOAD = "\033[38;2;125;207;255m"  # cyan
C_OK = "\033[38;2;115;218;202m"    # mint
C_WARN = "\033[38;2;224;175;104m"  # amber
C_HOT = "\033[38;2;247;118;142m"   # coral
C_TRK = "\033[38;2;74;80;104m"     # empty track
C_VAL = "\033[38;2;169;177;214m"   # value text
C_DIM = "\033[38;2;115;122;162m"   # dim text


def disp_width(s):
    return sum(2 if unicodedata.east_asian_width(c) in "WF" else 1 for c in s)


def pad_left(s, width):
    return " " * max(0, width - disp_width(s)) + s


def read_cpu_ticks():
    libc = ctypes.CDLL("/usr/lib/libSystem.B.dylib")
    HOST_CPU_LOAD_INFO = 3
    HOST_CPU_LOAD_INFO_COUNT = 4
    libc.mach_host_self.restype = ctypes.c_uint32
    libc.host_statistics.argtypes = [
        ctypes.c_uint32,
        ctypes.c_int,
        ctypes.POINTER(ctypes.c_uint32),
        ctypes.POINTER(ctypes.c_uint32),
    ]
    libc.host_statistics.restype = ctypes.c_int
    buf = (ctypes.c_uint32 * HOST_CPU_LOAD_INFO_COUNT)()
    cnt = ctypes.c_uint32(HOST_CPU_LOAD_INFO_COUNT)
    if libc.host_statistics(libc.mach_host_self(), HOST_CPU_LOAD_INFO, buf, ctypes.byref(cnt)) != 0:
        return None
    return [int(buf[i]) for i in range(4)]


def calc_cpu_pct(prev, cur):
    d = [(cur[i] - prev[i]) & 0xFFFFFFFF for i in range(4)]
    total = sum(d)
    if total <= 0:
        return None
    used = d[0] + d[1] + d[3]  # user + system + nice
    return max(0.0, min(100.0, used / total * 100.0))


def get_cpu_pct():
    state_path = os.path.join(tempfile.gettempdir(), f"ikesu-cpu-ticks-{os.getuid()}.json")
    now = time.time()
    cur = read_cpu_ticks()
    if cur is None:
        return 0.0

    pct = None
    prev_state = None
    try:
        with open(state_path, "r", encoding="utf-8") as f:
            prev_state = json.load(f)
    except Exception:
        prev_state = None

    if prev_state and isinstance(prev_state.get("ticks"), list) and len(prev_state["ticks"]) == 4:
        age = now - float(prev_state.get("ts", 0))
        if 2.0 <= age <= 180.0:
            pct = calc_cpu_pct(prev_state["ticks"], cur)
        elif 0.0 <= age < 2.0 and prev_state.get("pct") is not None:
            # Immediate re-run (e.g. sidebar resize): reuse fresh sample.
            return float(prev_state["pct"])

    if pct is None:
        time.sleep(0.25)
        cur2 = read_cpu_ticks()
        if cur2 is not None:
            pct = calc_cpu_pct(cur, cur2)
            cur = cur2

    pct = 0.0 if pct is None else pct
    try:
        tmp = f"{state_path}.{os.getpid()}"
        with open(tmp, "w", encoding="utf-8") as f:
            json.dump({"ts": time.time(), "ticks": cur, "pct": pct}, f)
        os.replace(tmp, state_path)
    except Exception:
        pass
    return pct


def sysctl_str(key):
    return subprocess.check_output(["/usr/sbin/sysctl", "-n", key], text=True).strip()


def get_mem():
    total = int(sysctl_str("hw.memsize"))
    pagesize = int(sysctl_str("hw.pagesize"))
    vm = subprocess.check_output(["/usr/bin/vm_stat"], text=True)
    active = wired = comp = 0
    for line in vm.splitlines():
        if "Pages active:" in line:
            active = int(line.split()[-1].rstrip("."))
        elif "Pages wired down:" in line:
            wired = int(line.split()[-1].rstrip("."))
        elif "occupied by compressor:" in line:
            comp = int(line.split()[-1].rstrip("."))
    used = (active + wired + comp) * pagesize
    pct = max(0.0, min(100.0, used / total * 100.0)) if total > 0 else 0.0
    return used, total, pct


def get_disk():
    vol = "/System/Volumes/Data" if os.path.isdir("/System/Volumes/Data") else "/"
    out = subprocess.check_output(["/bin/df", "-k", vol], text=True).splitlines()[1].split()
    used_k, avail_k = int(out[2]), int(out[3])
    total_k = used_k + avail_k
    pct = max(0.0, min(100.0, used_k / total_k * 100.0)) if total_k > 0 else 0.0
    free_gb = avail_k / 1048576.0
    return free_gb, pct


def fmt_free_disk(free_gb):
    if free_gb >= 1000:
        return f"{free_gb / 1024:.1f}T空"
    return f"{free_gb:.0f}G空"


def bar_color(pct, warn_at, hot_at):
    if pct >= hot_at:
        return C_HOT
    if pct >= warn_at:
        return C_WARN
    return C_OK


def render_bar(pct, width, warn_at, hot_at):
    clamped = max(0.0, min(100.0, pct))
    fill = int(clamped / 100.0 * width + 0.5)
    if clamped > 0 and fill == 0:
        fill = 1
    fill = min(width, fill)
    empty = width - fill
    fc = bar_color(pct, warn_at, hot_at)
    out = ""
    if fill:
        out += f"{fc}{'█' * fill}{OFF}"
    if empty:
        out += f"{C_TRK}{'░' * empty}{OFF}"
    return out


ncpu = int(sysctl_str("hw.ncpu"))
l1, l5, l15 = os.getloadavg()
cpu_pct = get_cpu_pct()
mem_used, mem_total, mem_pct = get_mem()
disk_free_gb, disk_pct = get_disk()

mem_u_g = mem_used / 1073741824.0
mem_t_g = mem_total / 1073741824.0

rows = [
    (C_CPU, "CPU ", f"{ncpu}コア", cpu_pct, 60, 85),
    (C_MEM, "MEM ", f"{mem_u_g:.0f}/{mem_t_g:.0f}G", mem_pct, 65, 85),
    (C_DISK, "DISK", fmt_free_disk(disk_free_gb), disk_pct, 75, 90),
]

val_w = max(disp_width(r[2]) for r in rows)
# Layout with inline value:
# label(4) + " "(1) + val(val_w) + " "(1) + bar(bar_w) + " "(1) + "%3.0f%%"(4) = COLS
show_val = COLS >= (11 + val_w + 4)
bar_w = (COLS - 11 - val_w) if show_val else max(4, COLS - 10)

for lc, label, val_txt, pct, warn_at, hot_at in rows:
    bar = render_bar(pct, bar_w, warn_at, hot_at)
    vc = C_HOT if pct >= hot_at else (C_WARN if pct >= warn_at else C_VAL)
    num = f"{pct:3.0f}%"
    if show_val:
        val_col = pad_left(val_txt, val_w)
        print(f"{lc}{label}{OFF} {C_DIM}{val_col}{OFF} {bar} {vc}{num}{OFF}")
    else:
        print(f"{lc}{label}{OFF} {bar} {vc}{num}{OFF}")

# Load average line (1m / 5m / 15m)
l1_c = C_HOT if l1 >= ncpu * 1.2 else (C_WARN if l1 >= ncpu * 0.7 else C_VAL)
load_wide = f"{l1:.1f} · {l5:.1f} · {l15:.1f}"
if 5 + disp_width(load_wide) <= COLS:
    print(f"{C_LOAD}LOAD{OFF} {l1_c}{l1:.1f}{OFF}{C_DIM} · {OFF}{C_VAL}{l5:.1f}{OFF}{C_DIM} · {OFF}{C_DIM}{l15:.1f}{OFF}")
else:
    print(f"{C_LOAD}LOAD{OFF} {l1_c}{l1:.1f}{OFF} {C_VAL}{l5:.1f}{OFF} {C_DIM}{l15:.1f}{OFF}")
PY
