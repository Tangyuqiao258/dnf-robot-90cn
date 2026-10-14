# Soak: run a long, high-online robot load against a live 90CN server

One entry point for the whole soak loop. Nothing about a run lives in a shell
history or a scratch directory: the tier, the pid, the guard log and the config
backup all sit in `<RobotDir>\soak\`, so a run started tonight is still
findable and stoppable tomorrow morning.

## Usage

```powershell
# start a 100-tier soak (writes the tier, launches detached, arms the guard)
powershell -NoProfile -ExecutionPolicy RemoteSigned -File deploy\soak\soak.ps1 start 100

# look at it
powershell -NoProfile -File deploy\soak\soak.ps1 status

# stop the robot and the guard
powershell -NoProfile -File deploy\soak\soak.ps1 stop
```

Defaults: `-RobotDir G:\90-CN\robot-run`, config at
`<RobotDir>\config\conf\robot_config.ini`, guard thresholds
`-FreeFloorMB 3500` / `-WsCeilMB 4096`.

| action | what it does |
| --- | --- |
| `start [tier]` | backs up the ini, writes `auto_target_online_count`, launches `robot.exe` detached, arms the memory guard |
| `status` | tier, robot pid + working set, guard count, guard log tail, last metrics line, online histogram |
| `stop` | kills the guard(s) and the robot, prints the final metrics line |
| `guard` | internal — the detached loop `start` spawns; not meant to be called by hand |

The robot and the guard are both started detached, so closing the shell that
started a soak does not stop it. `start` refuses to run if a robot from a
previous soak is still alive — `stop` first.

## Reading the logs

State directory `<RobotDir>\soak\`:

| file | what |
| --- | --- |
| `soak.pid` | pid of the robot this run launched |
| `guard.log` | one `ok free=…MB soak_ws=…MB` line every 10 min, or a `TRIP` line and the stop it performed |
| `robot-out.log` / `robot-err.log` | stdout / stderr of `robot.exe` |
| `robot_config.ini.bak-<stamp>` | the config as it was before `start` rewrote the tier |

The robot's own log is `<RobotDir>\config\logs\robot.log` (appended to across
runs; it is not rotated per run, so filter by timestamp).

### `runtime running` is the online population

In a `[RobotMetrics]` line, three fields look like the online count and only
one of them is:

```
[14/10/26 05:01:20] [RobotMetrics] policy=store_expand target=100 actors=100
  leased=100 idle=0 state idle=0 assigned=0 online=0 running=69 busy=31
  releasing=0 runtime running=100 store=0 connecting=0 recycling=0 blocked=0
  cpu=2.3 mem_mb=1665 goroutines=415 online=100/0 online_window=11/0 ...
```

- `runtime running=<n>` — **this is the live online population.**
- `online=<n>` — `counts.StateOnline`, a transient state bucket (0 here while
  100 are online).
- `online=<a>/<b>` — `stats.OnlineSuccess`/`stats.OnlineFailed`, cumulative
  counters since start (100/0 = 100 logins ever succeeded).

The line is built in `internal/scheduler/auto_metrics.go`; check it there
before trusting any field name. Guessing from the name has produced wrong
numbers on this project more than once.

`soak.ps1 status` prints a histogram of `runtime running` over the last 4000
metrics lines for exactly this reason.

## Reference: the 2026-10-14 20/100-tier soak

The run that cleared the B3 acceptance item, as measured:

- 20 tier 00:16–05:00, 100 tier 05:01–09:34; the 100 tier ran **4h20m** as
  observed by the guard (05:14:30 → 09:34:33) and 4h34m of metrics samples
  (05:01:09 → 09:34:59). Both clear the 1-hour acceptance bar.
- Memory: 27 guard samples, all `ok`, threshold never tripped. `soak_ws`
  settled at 1225–1251MB after a 1756MB startup transient → no leak. System
  free memory never below 5287MB.
- Online (`runtime running`, 1554 samples): peak **100** (full tier) — first at
  05:01:20, last at 09:20:12; steady band 90–98, low 86.
- It ended by **power loss**, not a planned stop, and it was **not** the
  scheduled soak duration. State that plainly if the run is ever cited.
