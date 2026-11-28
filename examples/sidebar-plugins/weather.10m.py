#!/usr/bin/python3 -I
# ikesu.position = right
# ikesu.title = 天気
# ikesu.order = 10
# Shows real-time current weather, today's min/max temperatures, and 2-hourly
# precipitation trend via Open-Meteo. Edit LAT/LON (or set WEATHER_LAT /
# WEATHER_LON) to match your location; defaults to Tokyo.
import json
import os
import unicodedata
import urllib.request

LAT = os.environ.get("WEATHER_LAT", "35.6812")  # Tokyo Station
LON = os.environ.get("WEATHER_LON", "139.7671")
URL = (
    "https://api.open-meteo.com/v1/forecast"
    f"?latitude={LAT}&longitude={LON}"
    "&current=temperature_2m,relative_humidity_2m,precipitation,weather_code,wind_speed_10m"
    "&hourly=precipitation_probability"
    "&daily=temperature_2m_max,temperature_2m_min"
    "&wind_speed_unit=ms&timezone=auto&forecast_days=2"
)

COLUMNS = max(18, int(os.environ.get("IKESU_SIDEBAR_COLUMNS") or 30))

OFF = "\033[0m"
BOLD = "\033[1m"
C_TEXT = "\033[38;2;230;234;245m"
C_VAL = "\033[38;2;169;177;214m"
C_DIM = "\033[38;2;115;122;162m"
C_TRK = "\033[38;2;74;80;104m"
C_SLATE = "\033[38;2;104;114;148m"
C_PEACH = "\033[38;2;224;160;139m"
C_BLUE = "\033[38;2;137;180;250m"
C_CYAN = "\033[38;2;125;207;255m"
C_MINT = "\033[38;2;115;218;202m"
C_YELLOW = "\033[38;2;224;175;104m"
C_RED = "\033[38;2;247;118;142m"


def disp_width(s):
    return sum(2 if unicodedata.east_asian_width(c) in "WF" else 1 for c in s)


def temp_color(val):
    if val is None:
        return C_DIM
    if val <= 5:
        return C_BLUE
    if val <= 15:
        return C_MINT
    if val <= 24:
        return C_YELLOW
    return C_RED


def pop_color(pop):
    if pop is None:
        return C_DIM
    if pop >= 60:
        return C_CYAN
    if pop >= 30:
        return C_BLUE
    return C_SLATE


def format_wmo(code):
    if code == 0:
        return "快晴", f"{C_YELLOW}快晴{OFF}"
    if code == 1:
        return "晴れ", f"{C_YELLOW}晴れ{OFF}"
    if code == 2:
        return "晴曇", f"{C_YELLOW}晴{OFF}{C_VAL}曇{OFF}"
    if code == 3:
        return "曇り", f"{C_VAL}曇り{OFF}"
    if code in (45, 48):
        return "濃霧", f"{C_DIM}濃霧{OFF}"
    if code in (51, 53, 55, 56, 57):
        return "霧雨", f"{C_BLUE}霧雨{OFF}"
    if code in (61, 63, 66, 80, 81):
        return "雨", f"{C_CYAN}雨{OFF}"
    if code in (65, 67, 82):
        return "大雨", f"{BOLD}{C_CYAN}大雨{OFF}"
    if code in (71, 73, 77, 85):
        return "雪", f"{C_TEXT}雪{OFF}"
    if code in (75, 86):
        return "大雪", f"{BOLD}{C_TEXT}大雪{OFF}"
    if code in (95, 96, 99):
        return "雷雨", f"{BOLD}{C_YELLOW}雷雨{OFF}"
    return "不明", f"{C_DIM}不明{OFF}"


def spark_char(pop):
    if pop is None or pop <= 0:
        return f"{C_TRK}·{OFF}"
    blocks = "▁▂▃▄▅▆▇█"
    idx = min(len(blocks) - 1, max(0, int((pop - 1) / 100 * len(blocks))))
    return f"{pop_color(pop)}{blocks[idx]}{OFF}"


try:
    req = urllib.request.Request(URL, headers={"User-Agent": "ikesu-sidebar"})
    with urllib.request.urlopen(req, timeout=4) as r:
        data = json.loads(r.read().decode())

    cur = data.get("current", {})
    hourly = data.get("hourly", {})
    daily = data.get("daily", {})

    lines = []

    # 1. Current real-time weather
    w_plain, w_styled = format_wmo(cur.get("weather_code"))
    left_plain = f"現在 {w_plain}"
    left_styled = f"{C_CYAN}現在{OFF} {w_styled}"

    temp = cur.get("temperature_2m")
    if temp is not None:
        t_txt = f"{temp:.1f}℃"
        left_plain += " " + t_txt
        left_styled += f" {BOLD}{temp_color(temp)}{t_txt}{OFF}"

    rain = cur.get("precipitation") or 0.0
    hum = cur.get("relative_humidity_2m")
    wind = cur.get("wind_speed_10m")

    if rain > 0:
        r_txt = f"雨{rain:.1f}mm"
        if disp_width(left_plain) + 1 + disp_width(r_txt) <= COLUMNS:
            left_plain += " " + r_txt
            left_styled += f" {C_CYAN}{r_txt}{OFF}"
    elif hum is not None:
        h_txt = f"湿{int(round(hum))}%"
        if disp_width(left_plain) + 1 + disp_width(h_txt) <= COLUMNS:
            left_plain += " " + h_txt
            left_styled += f" {C_VAL}{h_txt}{OFF}"

    if wind is not None and rain <= 0:
        w_txt = f"風{int(round(wind))}m"
        if disp_width(left_plain) + 1 + disp_width(w_txt) <= COLUMNS:
            left_plain += " " + w_txt
            left_styled += f" {C_DIM}{w_txt}{OFF}"

    lines.append(left_styled)

    # 2. Today's min/max temperature (explicitly labeled)
    d_mins = daily.get("temperature_2m_min", [])
    d_maxs = daily.get("temperature_2m_max", [])
    tmin = d_mins[0] if d_mins else None
    tmax = d_maxs[0] if d_maxs else None
    if tmin is not None and tmax is not None:
        s_min = f"{int(round(tmin))}℃"
        s_max = f"{int(round(tmax))}℃"
        full_plain = f"気温 最低{s_min} / 最高{s_max}"
        if disp_width(full_plain) <= COLUMNS:
            lines.append(
                f"{C_PEACH}気温{OFF} {C_DIM}最低{OFF}{temp_color(tmin)}{s_min}{OFF}"
                f"{C_DIM} / 最高{OFF}{temp_color(tmax)}{s_max}{OFF}"
            )
        else:
            lines.append(
                f"{C_PEACH}気温{OFF} {C_DIM}低{OFF}{temp_color(tmin)}{s_min}{OFF}"
                f"{C_DIM} / 高{OFF}{temp_color(tmax)}{s_max}{OFF}"
            )

    # 3. 2-hourly precipitation trend starting from the next hour
    cur_hour_key = cur.get("time", "")[:13]
    h_times = hourly.get("time", [])
    h_pops = hourly.get("precipitation_probability", [])
    start_idx = next((i for i, t in enumerate(h_times) if t[:13] >= cur_hour_key), 0)
    slot_start = start_idx + 1 if start_idx + 1 < len(h_times) else start_idx

    parts_styled = []
    used_w = disp_width("降水")
    for i in range(slot_start, len(h_times), 2):
        hh = h_times[i][11:13]
        pv = h_pops[i]
        if used_w + 1 + 3 > COLUMNS:
            break
        parts_styled.append(f"{C_DIM}{hh}{OFF}{spark_char(pv)}")
        used_w += 1 + 3
    if parts_styled:
        lines.append(f"{C_BLUE}降水{OFF} {' '.join(parts_styled)}")

    print("\n".join(lines))
except Exception as e:
    print(f"{C_DIM}天気 取得失敗 ({type(e).__name__}){OFF}")
