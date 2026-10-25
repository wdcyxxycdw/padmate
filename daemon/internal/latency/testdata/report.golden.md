## 端到端延迟（ENG-11）

**gate: evaluated**

- Date: 2026-09-25T01:02:03Z
- Go: go1.27.0
- OS: windows 10.0.26200
- CPUs: 16, GOMAXPROCS 16
- Counter: 10000000 Hz
- SDL 3.4.16
- SDL_TIMER_RESOLUTION: unset
- Scale: 1

Calibration: width 0.1 µs; SDL 10000000 Hz = QPC 10000000 Hz; start and end agree

| Row | Gap | Sender | Events | N | p50 | p90 | p99 | p99.9 | max | GCs | Target | Result |
|---|---:|---|---|---:|---:|---:|---:|---:|---:|---:|---|---|
| A | 5ms | SendInput | 1+3 | 2000 | 110.0 µs | 180.0 µs | 301.2 µs | 999.9 µs | 1.234 ms | 3 | p99 < 1ms | pass |
| B | 250ms | SendInput | 1 | 2000 | 900.0 µs | 1.500 ms | 2.345 ms | 3.000 ms | 12.000 ms | 0 | p99 < 2ms | FAIL |
| C | 50ms | SendInput | 1+3 | 400 | 200.0 µs | 400.0 µs | 1.499 ms | – | 1.700 ms | 0 | p99 < 1.5ms | pass |
| H | 1s | SendInput | 3 | 40 | 0.0 µs | 0.0 µs | 999.9 µs | – | 1.000 ms | 0 | – | info |

- B: C-state exit
- H: four pads at once; info only

### Split by events per Send

| Row | Gap | Sender | Events | N | p50 | p90 | p99 | p99.9 | max | GCs |
|---|---:|---|---|---:|---:|---:|---:|---:|---:|---:|
| A1 | 5ms | SendInput | 1 | 1000 | 100.0 µs | 150.0 µs | 250.0 µs | 400.0 µs | 500.0 µs | 0 |
| A3 | 5ms | SendInput | 3 | 1000 | 120.0 µs | 190.0 µs | 320.0 µs | 900.0 µs | 1.234 ms | 0 |

### Stages

| Row | Stage | p50 | p99 | max |
|---|---|---:|---:|---:|
| A | sdl→slot | 12.3 µs | 35.0 µs | 120.0 µs |
| A | slot→send | 3.2 µs | 6.5 µs | 15.0 µs |
| A | send→hook | 40.0 µs | 70.0 µs | 1.450 ms |

A: hook ran before SendInput returned: 1998 of 2000

B: hook ran before SendInput returned: 0 of 2000

C: hook ran before SendInput returned: 400 of 400

Notes:

- L is t_ret − t_sdl.

---

## 端到端延迟（ENG-11）

**GATE NOT EVALUATED** (skipped): no injection (PADMATE_TEST_INJECT unset)

- Date: 2026-09-25T01:02:03Z
- Go: go1.27.0
- OS: windows 10.0.26200
- CPUs: 16, GOMAXPROCS 16
- Counter: 10000000 Hz
- SDL 3.4.16
- SDL_TIMER_RESOLUTION: unset
- Scale: 1

Calibration: width 0.1 µs

| Row | Gap | Sender | Events | N | p50 | p90 | p99 | p99.9 | max | GCs | Target | Result |
|---|---:|---|---|---:|---:|---:|---:|---:|---:|---:|---|---|
| A | 5ms | no-op | 1+3 | 40 | 10.0 µs | 20.0 µs | 30.0 µs | – | 50.0 µs | 0 | p99 < 1ms | not evaluated |
| Z | 0s | no-op | 1 | 0 | – | – | – | – | – | 0 | p99 < 1ms | not evaluated |

### Stages

| Row | Stage | p50 | p99 | max |
|---|---|---:|---:|---:|
| A | sdl→slot | 12.3 µs | 35.0 µs | 120.0 µs |
| A | slot→send | 3.2 µs | 6.5 µs | 15.0 µs |

Notes:

- No SendInput: the rows time the no-op sender.

---

## SendInput 单独（ENG-11）

**GATE NOT EVALUATED** (off): info only

- Date: 2026-09-25T01:02:03Z
- Go: go1.27.0
- OS: linux
- CPUs: 4, GOMAXPROCS 2
- Counter: 1000000000 Hz

| Row | Gap | Sender | Events | N | p50 | p90 | p99 | p99.9 | max | GCs | Target | Result |
|---|---:|---|---|---:|---:|---:|---:|---:|---:|---:|---|---|
| S1 | 0s | SendInput | 1 | 5000 | 20.0 µs | 30.0 µs | 45.5 µs | 80.0 µs | 2.000 ms | 0 | – | info |
