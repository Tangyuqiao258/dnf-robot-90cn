#requires -Version 5.1
<#
.SYNOPSIS
  Run, watch and stop a robot soak on a live 90CN server.

.DESCRIPTION
  One entry point for the whole soak loop, so nothing lives in a scratch
  directory:

    soak.ps1 start 100     set the online target, launch robot.exe detached,
                           arm the memory guard
    soak.ps1 status        tier, pid, guard tail, last metrics line, online trend
    soak.ps1 stop          stop the guard and the robot, print the final metrics

  The robot is launched detached from this process: closing the shell that
  started the soak does not stop it. The guard runs the same way.

  State (pid file, guard log, robot output, config backups) goes to
  <RobotDir>\soak\ so everything about a run is in one findable place.

.PARAMETER Tier
  Value written to auto_target_online_count. Only used by `start`.

.PARAMETER RobotDir
  Deployed robot directory (holds robot.exe and config\).

.PARAMETER IniPath
  Defaults to <RobotDir>\config\conf\robot_config.ini.

.PARAMETER NoGuard
  Start without the memory guard. Only for short manual runs.

.EXAMPLE
  powershell -NoProfile -File deploy\soak\soak.ps1 start 100
  powershell -NoProfile -File deploy\soak\soak.ps1 status
  powershell -NoProfile -File deploy\soak\soak.ps1 stop
#>
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('start', 'stop', 'status', 'guard')]
    [string]$Action = 'status',

    [Parameter(Position = 1)]
    [int]$Tier = 100,

    [string]$RobotDir = 'G:\90-CN\robot-run',
    [string]$IniPath,
    [int]$FreeFloorMB = 3500,
    [int]$WsCeilMB = 4096,

    # Internal: `start` re-invokes this script as the detached guard with these set.
    [int]$GuardPid = 0,
    [string]$StateDir,
    [switch]$NoGuard
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2.0

if (-not $StateDir) { $StateDir = Join-Path $RobotDir 'soak' }
if (-not $IniPath)  { $IniPath  = Join-Path $RobotDir 'config\conf\robot_config.ini' }

$PidFile     = Join-Path $StateDir 'soak.pid'
$GuardLog    = Join-Path $StateDir 'guard.log'
$RobotOut    = Join-Path $StateDir 'robot-out.log'
$RobotErr    = Join-Path $StateDir 'robot-err.log'
$RobotLog    = Join-Path $RobotDir 'config\logs\robot.log'
$RobotExe    = Join-Path $RobotDir 'robot.exe'

function Write-Head($text) { Write-Host "=== $text ===" }

function Get-SoakPid {
    if (-not (Test-Path $PidFile)) { return 0 }
    $raw = (Get-Content $PidFile -First 1 -ErrorAction SilentlyContinue)
    $parsed = 0
    if ([int]::TryParse($raw, [ref]$parsed)) { return $parsed }
    return 0
}

function Test-Pid($procId) {
    if ($procId -le 0) { return $false }
    return $null -ne (Get-Process -Id $procId -ErrorAction SilentlyContinue)
}

function Get-GuardProcesses {
    # Match on this script's own path, never on a bare 'guard' substring: an
    # empty pattern would match every powershell on the box and stop would
    # then kill unrelated processes.
    Get-CimInstance Win32_Process -Filter "Name='powershell.exe'" -ErrorAction SilentlyContinue |
        Where-Object { $_.CommandLine -and $_.CommandLine -like "*$PSCommandPath*" -and $_.CommandLine -like '*guard*' }
}

function Get-LastMetricsLine {
    if (-not (Test-Path $RobotLog)) { return '(no robot.log yet)' }
    # -match, not -like: in a wildcard pattern [RobotMetrics] is a character
    # class, so -like would match any line holding one of those letters.
    $line = Get-Content $RobotLog -Tail 400 -ErrorAction SilentlyContinue |
        Where-Object { $_ -match '\[RobotMetrics\]' } | Select-Object -Last 1
    if (-not $line) { return '(no [RobotMetrics] line in the tail)' }
    return $line
}

function Show-OnlineTrend {
    # `runtime running` is the live online population. Two other fields in the
    # same line are easy to mistake for it and are NOT:
    #   online=<n>      -> counts.StateOnline, a transient state bucket
    #   online=<a>/<b>  -> stats.OnlineSuccess/OnlineFailed, cumulative counters
    # See internal/scheduler/auto_metrics.go where the line is built.
    $tail = Get-Content $RobotLog -Tail 4000 -ErrorAction SilentlyContinue |
        Where-Object { $_ -match '\[RobotMetrics\]' }
    if (-not $tail) { Write-Host '  (no samples)'; return }
    $hist = @{}
    foreach ($line in $tail) {
        $fields = $line -split '\s+'
        for ($i = 0; $i -lt $fields.Count - 1; $i++) {
            if ($fields[$i] -eq 'runtime' -and $fields[$i + 1] -like 'running=*') {
                $n = [int]($fields[$i + 1] -replace 'running=', '')
                if ($hist.ContainsKey($n)) { $hist[$n]++ } else { $hist[$n] = 1 }
                break
            }
        }
    }
    $hist.GetEnumerator() | Sort-Object Name | ForEach-Object {
        Write-Host ("  runtime running={0,-5} {1} samples" -f $_.Name, $_.Value)
    }
}

function Invoke-Start {
    Write-Head 'start'
    if (-not (Test-Path $RobotExe)) { throw "robot.exe not found: $RobotExe" }
    if (-not (Test-Path $IniPath))  { throw "config not found: $IniPath" }

    $existing = Get-SoakPid
    if (Test-Pid $existing) {
        throw "a robot is already running (pid $existing). Run 'soak.ps1 stop' first."
    }

    New-Item -ItemType Directory -Force -Path $StateDir | Out-Null

    # Back up before touching the config, always, even when the value is unchanged.
    $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
    $backup = Join-Path $StateDir "robot_config.ini.bak-$stamp"
    Copy-Item -Force $IniPath $backup
    Write-Host "backup     : $backup"

    $text = Get-Content -Raw $IniPath
    # Presence is tested on its own: a replacement that lands the same text (the
    # tier already equals $Tier) must not read as "key missing".
    if (-not [regex]::IsMatch($text, '(?m)^auto_target_online_count\s*=')) {
        throw 'auto_target_online_count was not found in the config; refusing to guess.'
    }
    # [^\r\n]* rather than .* so the CR of a CRLF line is not swallowed into
    # the match and dropped on rewrite; the config keeps its line endings.
    $new  = [regex]::Replace($text, '(?m)^auto_target_online_count\s*=[^\r\n]*', "auto_target_online_count = $Tier")
    Set-Content -Path $IniPath -Value $new -NoNewline -Encoding ASCII
    $current = (Select-String -Path $IniPath -Pattern '^\s*auto_target_online_count\s*=' | Select-Object -First 1).Line
    Write-Host "tier       : $($current.Trim())"

    $proc = Start-Process -FilePath $RobotExe -WorkingDirectory $RobotDir -PassThru -WindowStyle Hidden `
        -RedirectStandardOutput $RobotOut -RedirectStandardError $RobotErr
    Set-Content -Path $PidFile -Value $proc.Id -Encoding ASCII
    Write-Host "robot pid  : $($proc.Id)"
    Write-Host "robot out  : $RobotOut"

    if (-not $NoGuard) {
        $guardArgs = @(
            '-NoProfile', '-ExecutionPolicy', 'RemoteSigned', '-File', $PSCommandPath,
            'guard', '-GuardPid', $proc.Id, '-RobotDir', $RobotDir, '-StateDir', $StateDir,
            '-FreeFloorMB', $FreeFloorMB, '-WsCeilMB', $WsCeilMB
        )
        $guard = Start-Process powershell -ArgumentList $guardArgs -PassThru -WindowStyle Hidden
        Write-Host "guard pid  : $($guard.Id)  (free floor ${FreeFloorMB}MB, ws ceiling ${WsCeilMB}MB)"
        Write-Host "guard log  : $GuardLog"
    } else {
        Write-Host 'guard      : skipped (-NoGuard)'
    }

    Write-Host ''
    Write-Host "Started. Watch it with:  powershell -NoProfile -File `"$PSCommandPath`" status"
}

function Invoke-Guard {
    "[{0}] guard armed pid={1} freeFloor={2}MB wsCeil={3}MB" -f (Get-Date -f 'HH:mm:ss'), $GuardPid, $FreeFloorMB, $WsCeilMB |
        Out-File -FilePath $GuardLog -Encoding utf8 -Append
    while ($true) {
        $proc = Get-Process -Id $GuardPid -ErrorAction SilentlyContinue
        if (-not $proc) {
            "[{0}] soak pid={1} is gone; guard exits" -f (Get-Date -f 'HH:mm:ss'), $GuardPid |
                Out-File -FilePath $GuardLog -Encoding utf8 -Append
            break
        }
        $os     = Get-CimInstance Win32_OperatingSystem
        $freeMB = [int]($os.FreePhysicalMemory / 1KB)
        $wsMB   = [int]($proc.WorkingSet64 / 1MB)

        if ($freeMB -lt $FreeFloorMB -or $wsMB -gt $WsCeilMB) {
            "[{0}] TRIP free={1}MB ws={2}MB -> stopping soak pid={3}" -f (Get-Date -f 'HH:mm:ss'), $freeMB, $wsMB, $GuardPid |
                Out-File -FilePath $GuardLog -Encoding utf8 -Append
            Stop-Process -Id $GuardPid -Force
            Start-Sleep -Seconds 3
            $os2 = Get-CimInstance Win32_OperatingSystem
            "[{0}] soak stopped; free now {1}MB" -f (Get-Date -f 'HH:mm:ss'), [int]($os2.FreePhysicalMemory / 1KB) |
                Out-File -FilePath $GuardLog -Encoding utf8 -Append
            break
        }
        # Heartbeat every ~10 minutes so an unattended run leaves a readable trend.
        "[{0}] ok free={1}MB soak_ws={2}MB" -f (Get-Date -f 'HH:mm:ss'), $freeMB, $wsMB |
            Out-File -FilePath $GuardLog -Encoding utf8 -Append
        Start-Sleep -Seconds 600
    }
}

function Invoke-Status {
    Write-Head 'status'
    if (Test-Path $IniPath) {
        $tierLine = (Select-String -Path $IniPath -Pattern '^\s*auto_target_online_count\s*=' | Select-Object -First 1).Line
        Write-Host "tier       : $($tierLine.Trim())"
    }

    $procId = Get-SoakPid
    if (Test-Pid $procId) {
        $p = Get-Process -Id $procId
        Write-Host ("robot      : pid {0} alive, ws={1}MB, cpu={2}s" -f $procId, [int]($p.WorkingSet64 / 1MB), [int]$p.CPU)
    } else {
        Write-Host "robot      : not running (pid file says $procId)"
    }

    $guards = @(Get-GuardProcesses)
    Write-Host "guard      : $($guards.Count) process(es)"

    if (Test-Path $GuardLog) {
        Write-Host 'guard tail :'
        Get-Content $GuardLog -Tail 3 -ErrorAction SilentlyContinue | ForEach-Object { Write-Host "  $_" }
    }

    Write-Host 'last metrics:'
    Write-Host "  $(Get-LastMetricsLine)"

    Write-Host 'online trend (tail of robot.log):'
    Show-OnlineTrend
}

function Invoke-Stop {
    Write-Head 'stop'

    foreach ($g in @(Get-GuardProcesses)) {
        Write-Host "stopping guard pid $($g.ProcessId)"
        Stop-Process -Id $g.ProcessId -Force -ErrorAction SilentlyContinue
    }

    $procId = Get-SoakPid
    if (Test-Pid $procId) {
        Write-Host "stopping robot pid $procId"
        Stop-Process -Id $procId -Force -ErrorAction SilentlyContinue
        Start-Sleep -Seconds 3
    } else {
        Write-Host 'robot was not running'
    }
    Remove-Item -Force $PidFile -ErrorAction SilentlyContinue

    Write-Host 'final metrics:'
    Write-Host "  $(Get-LastMetricsLine)"
}

switch ($Action) {
    'start'  { Invoke-Start }
    'stop'   { Invoke-Stop }
    'status' { Invoke-Status }
    'guard'  { Invoke-Guard }
}
