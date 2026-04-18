[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('baseline', 'start', 'status', 'stop', 'cleanup')]
    [string]$Action,

    [string]$RouterHost = $env:OPENWRT_ROUTER_HOST,

    [string]$RouterUser = $env:OPENWRT_ROUTER_USER,

    [string]$RouterPassword = $env:OPENWRT_ROUTER_PASSWORD,

    [string]$RouterHostKey = $env:OPENWRT_ROUTER_HOSTKEY,

    [ValidateSet('Auto', 'PuTTY', 'OpenSSH')]
    [string]$Transport = $(if ($env:OPENWRT_ROUTER_TRANSPORT) { $env:OPENWRT_ROUTER_TRANSPORT } else { 'Auto' }),

    [string]$OpenSshKnownHostsFile = $env:OPENWRT_ROUTER_KNOWN_HOSTS_FILE,

    [string]$OpenSshIdentityFile = $env:OPENWRT_ROUTER_IDENTITY_FILE,

    [string]$SessionId,

    [string]$LocalPath,

    [string]$RemoteCommand,

    [string]$ListenAddress = '127.0.0.1',

    [int]$Port,

    [int]$DurationSeconds = 900,

    [string]$ProcessPattern,

    [int]$LogLines = 40,

    [switch]$AllowLanBind,

    [switch]$AllowReservedPort,

    [switch]$AllowPortConflict,

    [switch]$UnsafeAllowMutatingCommand,

    [switch]$AsJson
)

$ErrorActionPreference = 'Stop'

. (Join-Path $PSScriptRoot 'OpenWrtSshTransport.ps1')

$SessionRoot = '/tmp/codex-test'
$ReservedPorts = @(22, 53, 67, 68, 80, 123, 443, 7681, 1070, 11400)
$DangerousCommandPatterns = @(
    '(^|[\s;|&])(opkg|apk)\b',
    '(^|[\s;|&])uci\b',
    '(^|[\s;|&])(fw4|iptables|ip6tables|nft)\b',
    '(^|[\s;|&])(sysupgrade|reboot|halt|poweroff|firstboot|jffs2reset)\b',
    '(^|[\s;|&])(mtd|ubiformat|ubiupdatevol|fw_setenv)\b',
    '(^|[\s;|&])(service|/etc/init\.d/)\b',
    '/etc/config/',
    '/overlay/',
    '/usr/bin/',
    '/usr/sbin/',
    '/etc/init\.d/',
    '/etc/uci-defaults/'
)

function Get-RequiredValue {
    param(
        [string]$Value,
        [string]$Name
    )

    if ([string]::IsNullOrWhiteSpace($Value)) {
        throw "Missing required value: $Name. Pass it as a parameter or set the corresponding OPENWRT_ROUTER_* environment variable."
    }

    return $Value
}

function New-PasswordFile {
    param([string]$Password)

    $tempRoot = [System.IO.Path]::GetTempPath()
    $tempFile = Join-Path $tempRoot ("putty-password-{0}.txt" -f ([guid]::NewGuid().ToString('N')))
    Set-Content -LiteralPath $tempFile -Value $Password -NoNewline
    return $tempFile
}

function ConvertTo-PosixSingleQuoted {
    param([string]$Value)

    if ($null -eq $Value) {
        return "''"
    }

    $replacement = "'" + '"' + "'" + '"' + "'"
    return "'" + $Value.Replace("'", $replacement) + "'"
}

function New-SafeSessionId {
    param(
        [switch]$RequireExisting
    )

    if ($SessionId) {
        if ($SessionId -notmatch '^[A-Za-z0-9._-]+$') {
            throw 'SessionId may contain only letters, digits, dot, underscore, and hyphen.'
        }

        return $SessionId
    }

    if ($RequireExisting) {
        throw 'SessionId is required for this action.'
    }

    return ('codex-{0}' -f (Get-Date -Format 'yyyyMMdd-HHmmss'))
}

function Test-IsReservedPort {
    param([int]$Value)

    return ($ReservedPorts -contains $Value) -or ($Value -gt 0 -and $Value -lt 1024)
}

function Assert-CommonRouterInputs {
    $script:RouterHost = Get-RequiredValue -Value $script:RouterHost -Name 'RouterHost'
    $script:RouterUser = Get-RequiredValue -Value $script:RouterUser -Name 'RouterUser'
}

function Assert-StartSafety {
    if ([string]::IsNullOrWhiteSpace($LocalPath)) {
        throw 'LocalPath is required for Action=start.'
    }

    if (-not (Test-Path -LiteralPath $LocalPath)) {
        throw "LocalPath does not exist: $LocalPath"
    }

    if ([string]::IsNullOrWhiteSpace($RemoteCommand)) {
        throw 'RemoteCommand is required for Action=start.'
    }

    if ($RemoteCommand -match "[`r`n]") {
        throw 'RemoteCommand must be a single line.'
    }

    if ($DurationSeconds -lt 30 -or $DurationSeconds -gt 3600) {
        throw 'DurationSeconds must stay within 30..3600 to keep tmp tests bounded.'
    }

    if (-not $AllowLanBind -and $ListenAddress -notin @('127.0.0.1', '::1', 'localhost')) {
        throw 'ListenAddress is restricted to loopback by default. Use -AllowLanBind only when the test explicitly needs LAN reachability.'
    }

    if ($PSBoundParameters.ContainsKey('Port') -and -not $AllowReservedPort -and (Test-IsReservedPort -Value $Port)) {
        throw "Port $Port is reserved or too privileged for the safe tmp harness."
    }

    if (-not $UnsafeAllowMutatingCommand) {
        foreach ($pattern in $DangerousCommandPatterns) {
            if ($RemoteCommand -match $pattern) {
                throw "RemoteCommand matched a blocked mutating pattern: $pattern"
            }
        }
    }
}

function Invoke-RemoteCommand {
    param(
        [psobject]$TransportSpec,
        [string]$CommandText
    )

    $response = Invoke-OpenWrtRemoteCommand -TransportSpec $TransportSpec -RouterHost $RouterHost -RouterUser $RouterUser -RouterPassword $RouterPassword -RouterHostKey $RouterHostKey -CommandText $CommandText -ViaStdinSh
    if ($response.exitCode -ne 0) {
        throw ($response.text + [Environment]::NewLine + "Remote command failed with exit code $($response.exitCode).")
    }

    return $response.output
}

function Invoke-RemoteUpload {
    param(
        [psobject]$TransportSpec,
        [string]$SourcePath,
        [string]$TargetPath
    )

    Copy-OpenWrtUpload -TransportSpec $TransportSpec -RouterHost $RouterHost -RouterUser $RouterUser -RouterPassword $RouterPassword -RouterHostKey $RouterHostKey -SourcePath $SourcePath -TargetPath $TargetPath
}

function Convert-MarkerOutput {
    param([string[]]$Lines)

    $markers = [ordered]@{}
    $plainLines = New-Object System.Collections.Generic.List[string]

    foreach ($line in $Lines) {
        if ($line -match '^__([A-Z0-9_]+)__=(.*)$') {
            $markers[$matches[1].ToLowerInvariant()] = $matches[2]
        }
        else {
            [void]$plainLines.Add($line)
        }
    }

    return [pscustomobject]@{
        markers = [pscustomobject]$markers
        plain_text = ($plainLines -join [Environment]::NewLine)
        plain_lines = @($plainLines)
    }
}

function Get-LocalArtifactInfo {
    param([string]$Path)

    $item = Get-Item -LiteralPath $Path
    $info = [ordered]@{
        source_path = $item.FullName
        source_name = $item.Name
        is_directory = $item.PSIsContainer
        local_sha256 = $null
    }

    if (-not $item.PSIsContainer) {
        $info.local_sha256 = (Get-FileHash -LiteralPath $item.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
    }

    return [pscustomobject]$info
}

function New-BaselineRemoteCommand {
    param(
        [int]$ProbePort,
        [string]$ProbePattern
    )

    $portSnippet = if ($PSBoundParameters.ContainsKey('ProbePort')) {
        @"
echo '--- port probe ---'
netstat -ltnp 2>/dev/null | grep -E ':$ProbePort[[:space:]]' || true
"@
    }
    else {
        ''
    }

    $patternSnippet = if ($ProbePattern) {
        $patternQuoted = ConvertTo-PosixSingleQuoted $ProbePattern
        @"
echo '--- process probe ---'
ps w | grep -F -- $patternQuoted | grep -v 'grep -F' || true
echo '--- log probe ---'
logread -l 80 2>/dev/null | grep -F -- $patternQuoted || true
"@
    }
    else {
        ''
    }

@"
echo '--- system board ---'
ubus call system board
echo '--- resources ---'
free -m 2>/dev/null || true
df -h /tmp /overlay 2>/dev/null || true
echo '--- listeners ---'
netstat -ltnp 2>/dev/null | head -n 25 || true
$portSnippet
$patternSnippet
"@
}

function Get-SessionDir {
    param([string]$Id)

    return "$SessionRoot/$Id"
}

function New-SessionStartCommand {
    param(
        [string]$Id,
        [string]$CommandLine,
        [string]$BindAddress,
        [int]$BindPort,
        [int]$LifetimeSeconds
    )

    $sessionDir = Get-SessionDir -Id $Id
    $payloadDir = "$sessionDir/payload"
    $commandFile = "$sessionDir/command.sh"
    $pidFile = "$sessionDir/pid"
    $watchdogPidFile = "$sessionDir/watchdog.pid"
    $logFile = "$sessionDir/stdout.log"
    $sessionMeta = "$sessionDir/session.env"

    $sessionDirQ = ConvertTo-PosixSingleQuoted $sessionDir
    $payloadDirQ = ConvertTo-PosixSingleQuoted $payloadDir
    $commandFileQ = ConvertTo-PosixSingleQuoted $commandFile
    $pidFileQ = ConvertTo-PosixSingleQuoted $pidFile
    $watchdogPidFileQ = ConvertTo-PosixSingleQuoted $watchdogPidFile
    $logFileQ = ConvertTo-PosixSingleQuoted $logFile
    $sessionMetaQ = ConvertTo-PosixSingleQuoted $sessionMeta
    $commandLiteralQ = ConvertTo-PosixSingleQuoted ("exec /bin/sh -c {0}" -f (ConvertTo-PosixSingleQuoted $CommandLine))
    $cdLiteralQ = ConvertTo-PosixSingleQuoted ("cd {0}" -f $payloadDir)
    $bindAddressQ = ConvertTo-PosixSingleQuoted $BindAddress
    $portValue = if ($PSBoundParameters.ContainsKey('BindPort') -and $BindPort -gt 0) { $BindPort.ToString() } else { '' }
    $sessionIdQ = ConvertTo-PosixSingleQuoted $Id

@"
set -eu
SESSION_ID=$sessionIdQ
SESSION_DIR=$sessionDirQ
PAYLOAD_DIR=$payloadDirQ
COMMAND_FILE=$commandFileQ
PID_FILE=$pidFileQ
WATCHDOG_PID_FILE=$watchdogPidFileQ
LOG_FILE=$logFileQ
SESSION_META=$sessionMetaQ
mkdir -p "`$SESSION_DIR" "`$PAYLOAD_DIR"
if [ -f "`$PID_FILE" ]; then
  OLD_PID=`$(cat "`$PID_FILE" 2>/dev/null || true)
  if [ -n "`$OLD_PID" ] && kill -0 "`$OLD_PID" 2>/dev/null; then
    echo '__ACTION__=start'
    echo "__SESSION_ID__=$Id"
    echo "__STATE__=already-running"
    echo "__PID__=`$OLD_PID"
    exit 1
  fi
fi
printf '%s\n' '#!/bin/sh' $cdLiteralQ $commandLiteralQ > "`$COMMAND_FILE"
chmod 700 "`$COMMAND_FILE"
printf '%s\n' "session_id=$Id" "listen_address=$BindAddress" "port=$portValue" "duration_seconds=$LifetimeSeconds" > "`$SESSION_META"
setsid nohup "`$COMMAND_FILE" > "`$LOG_FILE" 2>&1 < /dev/null &
APP_PID=`$!
echo "`$APP_PID" > "`$PID_FILE"
setsid nohup /bin/sh -c 'sleep $LifetimeSeconds; PID=`$(cat '"$pidFileQ"' 2>/dev/null || true); if [ -n "`$PID" ]; then kill "`$PID" 2>/dev/null || true; fi' >/dev/null 2>&1 < /dev/null &
WATCHDOG_PID=`$!
echo "`$WATCHDOG_PID" > "`$WATCHDOG_PID_FILE"
sleep 1
RUNNING=0
if kill -0 "`$APP_PID" 2>/dev/null; then
  RUNNING=1
fi
echo '__ACTION__=start'
echo "__SESSION_ID__=$Id"
echo "__SESSION_DIR__=$sessionDir"
echo "__PAYLOAD_DIR__=$payloadDir"
echo "__PID__=`$APP_PID"
echo "__WATCHDOG_PID__=`$WATCHDOG_PID"
echo "__RUNNING__=`$RUNNING"
echo "__LISTEN_ADDRESS__=$BindAddress"
echo "__PORT__=$portValue"
echo "__DURATION_SECONDS__=$LifetimeSeconds"
tail -n 20 "`$LOG_FILE" 2>/dev/null || true
"@
}

function New-SessionStatusCommand {
    param(
        [string]$Id,
        [int]$TailLines
    )

    $sessionDir = Get-SessionDir -Id $Id
    $sessionDirQ = ConvertTo-PosixSingleQuoted $sessionDir

@"
set -eu
SESSION_DIR=$sessionDirQ
echo '__ACTION__=status'
echo "__SESSION_ID__=$Id"
if [ ! -d "`$SESSION_DIR" ]; then
  echo '__STATE__=missing'
  exit 0
fi
PID=`$(cat "`$SESSION_DIR/pid" 2>/dev/null || true)
WATCHDOG_PID=`$(cat "`$SESSION_DIR/watchdog.pid" 2>/dev/null || true)
PORT=`$(grep '^port=' "`$SESSION_DIR/session.env" 2>/dev/null | cut -d= -f2- || true)
LISTEN_ADDRESS=`$(grep '^listen_address=' "`$SESSION_DIR/session.env" 2>/dev/null | cut -d= -f2- || true)
DURATION_SECONDS=`$(grep '^duration_seconds=' "`$SESSION_DIR/session.env" 2>/dev/null | cut -d= -f2- || true)
STATE=stopped
if [ -n "`$PID" ] && kill -0 "`$PID" 2>/dev/null; then
  STATE=running
fi
echo "__STATE__=`$STATE"
echo "__SESSION_DIR__=$sessionDir"
echo "__PID__=`$PID"
echo "__WATCHDOG_PID__=`$WATCHDOG_PID"
echo "__PORT__=`$PORT"
echo "__LISTEN_ADDRESS__=`$LISTEN_ADDRESS"
echo "__DURATION_SECONDS__=`$DURATION_SECONDS"
echo '--- process ---'
if [ -n "`$PID" ]; then
  ps w | grep -E "^[[:space:]]*`$PID " || true
fi
echo '--- port ---'
if [ -n "`$PORT" ]; then
  netstat -ltnp 2>/dev/null | grep -E ":`$PORT[[:space:]]" || true
fi
echo '--- log tail ---'
tail -n $TailLines "`$SESSION_DIR/stdout.log" 2>/dev/null || true
"@
}

function New-SessionStopCommand {
    param([string]$Id)

    $sessionDir = Get-SessionDir -Id $Id
    $sessionDirQ = ConvertTo-PosixSingleQuoted $sessionDir

@"
set -eu
SESSION_DIR=$sessionDirQ
echo '__ACTION__=stop'
echo "__SESSION_ID__=$Id"
if [ ! -d "`$SESSION_DIR" ]; then
  echo '__STATE__=missing'
  exit 0
fi
PID=`$(cat "`$SESSION_DIR/pid" 2>/dev/null || true)
WATCHDOG_PID=`$(cat "`$SESSION_DIR/watchdog.pid" 2>/dev/null || true)
if [ -n "`$PID" ]; then
  kill "`$PID" 2>/dev/null || true
fi
if [ -n "`$WATCHDOG_PID" ]; then
  kill "`$WATCHDOG_PID" 2>/dev/null || true
fi
echo '__STATE__=stopped'
echo "__PID__=`$PID"
echo "__WATCHDOG_PID__=`$WATCHDOG_PID"
"@
}

function New-SessionCleanupCommand {
    param([string]$Id)

    $sessionDir = Get-SessionDir -Id $Id
    $sessionDirQ = ConvertTo-PosixSingleQuoted $sessionDir

@"
set -eu
SESSION_DIR=$sessionDirQ
echo '__ACTION__=cleanup'
echo "__SESSION_ID__=$Id"
case "`$SESSION_DIR" in
  /tmp/codex-test/*) ;;
  *) echo '__STATE__=refused'; exit 1 ;;
esac
if [ ! -d "`$SESSION_DIR" ]; then
  echo '__STATE__=missing'
  exit 0
fi
PID=`$(cat "`$SESSION_DIR/pid" 2>/dev/null || true)
WATCHDOG_PID=`$(cat "`$SESSION_DIR/watchdog.pid" 2>/dev/null || true)
if [ -n "`$PID" ]; then
  kill "`$PID" 2>/dev/null || true
fi
if [ -n "`$WATCHDOG_PID" ]; then
  kill "`$WATCHDOG_PID" 2>/dev/null || true
fi
rm -rf "`$SESSION_DIR"
echo '__STATE__=cleaned'
"@
}

Assert-CommonRouterInputs

$transportSpec = Resolve-OpenWrtTransportSpec -Transport $Transport -RouterPassword $RouterPassword -RouterHostKey $RouterHostKey -OpenSshKnownHostsFile $OpenSshKnownHostsFile -OpenSshIdentityFile $OpenSshIdentityFile -NeedsUpload:($Action -eq 'start')
if ($transportSpec.mode -eq 'PuTTY') {
    $script:RouterPassword = Get-RequiredValue -Value $script:RouterPassword -Name 'RouterPassword'
    $script:RouterHostKey = Get-RequiredValue -Value $script:RouterHostKey -Name 'RouterHostKey'
}

$result = [ordered]@{
    action = $Action
    router_host = $RouterHost
    transport = $transportSpec.mode
    inventory_profile = 'tmp-test-harness'
    session_id = $null
    remote_session_dir = $null
    listen_address = $ListenAddress
    port = $null
    duration_seconds = $null
    artifact = $null
    state = $null
    safety_mode = 'guarded'
    raw_text = $null
}

switch ($Action) {
    'baseline' {
        $remoteCommand = New-BaselineRemoteCommand -ProbePort $Port -ProbePattern $ProcessPattern
        $lines = Invoke-RemoteCommand -TransportSpec $transportSpec -CommandText $remoteCommand
        $result.port = if ($PSBoundParameters.ContainsKey('Port')) { $Port } else { $null }
        $result.raw_text = ($lines -join [Environment]::NewLine)
        $result.state = 'read-only'
    }

    'start' {
        Assert-StartSafety
        $session = New-SafeSessionId
        $artifactInfo = Get-LocalArtifactInfo -Path $LocalPath
        $sessionDir = Get-SessionDir -Id $session
        $payloadDir = "$sessionDir/payload"

        $preflightPortSnippet = if ($PSBoundParameters.ContainsKey('Port') -and $Port -gt 0) {
            if ($AllowPortConflict) {
                ''
            }
            else {
                @"
if netstat -ltnp 2>/dev/null | grep -E ':$Port[[:space:]]' >/dev/null 2>&1; then
  echo '__ACTION__=preflight'
  echo "__SESSION_ID__=$session"
  echo '__STATE__=port-conflict'
  exit 1
fi
"@
            }
        }
        else {
            ''
        }

        $preflightCommand = @"
set -eu
mkdir -p $(ConvertTo-PosixSingleQuoted $payloadDir)
$preflightPortSnippet
echo '__ACTION__=preflight'
echo "__SESSION_ID__=$session"
echo '__STATE__=ok'
"@

        $preflightLines = Invoke-RemoteCommand -TransportSpec $transportSpec -CommandText $preflightCommand
        $preflightParsed = Convert-MarkerOutput -Lines $preflightLines
        if ($preflightParsed.markers.state -ne 'ok') {
            throw ($preflightParsed.plain_text + [Environment]::NewLine + 'Preflight failed.')
        }

        Invoke-RemoteUpload -TransportSpec $transportSpec -SourcePath $artifactInfo.source_path -TargetPath $payloadDir

        $startCommand = New-SessionStartCommand -Id $session -CommandLine $RemoteCommand -BindAddress $ListenAddress -BindPort $Port -LifetimeSeconds $DurationSeconds
        $startLines = Invoke-RemoteCommand -TransportSpec $transportSpec -CommandText $startCommand
        $startParsed = Convert-MarkerOutput -Lines $startLines

        $remoteUploadedEntry = "$payloadDir/$($artifactInfo.source_name)"
        $artifactResult = [ordered]@{
            local_path = $artifactInfo.source_path
            local_name = $artifactInfo.source_name
            is_directory = $artifactInfo.is_directory
            local_sha256 = $artifactInfo.local_sha256
            remote_uploaded_entry = $remoteUploadedEntry
        }

        if (-not $artifactInfo.is_directory) {
            $hashCommand = @"
sha256sum $(ConvertTo-PosixSingleQuoted $remoteUploadedEntry) 2>/dev/null | awk '{print $1}'
"@
            $remoteHash = (Invoke-RemoteCommand -TransportSpec $transportSpec -CommandText $hashCommand | Select-Object -First 1)
            $artifactResult.remote_sha256 = if ($remoteHash) { $remoteHash.Trim().ToLowerInvariant() } else { $null }
        }

        $result.session_id = $session
        $result.remote_session_dir = $sessionDir
        $result.listen_address = $ListenAddress
        $result.port = if ($PSBoundParameters.ContainsKey('Port')) { $Port } else { $null }
        $result.duration_seconds = $DurationSeconds
        $result.artifact = [pscustomobject]$artifactResult
        $result.state = if ($startParsed.markers.running -eq '1') { 'running' } else { 'failed-to-stay-up' }
        $result.raw_text = $startParsed.plain_text
    }

    'status' {
        $session = New-SafeSessionId -RequireExisting
        $lines = Invoke-RemoteCommand -TransportSpec $transportSpec -CommandText (New-SessionStatusCommand -Id $session -TailLines $LogLines)
        $parsed = Convert-MarkerOutput -Lines $lines
        $result.session_id = $session
        $result.remote_session_dir = Get-SessionDir -Id $session
        $result.state = $parsed.markers.state
        $result.listen_address = $parsed.markers.listen_address
        $result.port = if ($parsed.markers.port) { [int]$parsed.markers.port } else { $null }
        $result.duration_seconds = if ($parsed.markers.duration_seconds) { [int]$parsed.markers.duration_seconds } else { $null }
        $result.raw_text = $parsed.plain_text
    }

    'stop' {
        $session = New-SafeSessionId -RequireExisting
        $lines = Invoke-RemoteCommand -TransportSpec $transportSpec -CommandText (New-SessionStopCommand -Id $session)
        $parsed = Convert-MarkerOutput -Lines $lines
        $result.session_id = $session
        $result.remote_session_dir = Get-SessionDir -Id $session
        $result.state = $parsed.markers.state
        $result.raw_text = $parsed.plain_text
    }

    'cleanup' {
        $session = New-SafeSessionId -RequireExisting
        $lines = Invoke-RemoteCommand -TransportSpec $transportSpec -CommandText (New-SessionCleanupCommand -Id $session)
        $parsed = Convert-MarkerOutput -Lines $lines
        $result.session_id = $session
        $result.remote_session_dir = Get-SessionDir -Id $session
        $result.state = $parsed.markers.state
        $result.raw_text = $parsed.plain_text
    }
}

if ($AsJson) {
    [pscustomobject]$result | ConvertTo-Json -Depth 8
    exit 0
}

Write-Output 'OpenWrt Tmp Program Session'
Write-Output '==========================='
Write-Output ("Action: {0}" -f $result.action)
Write-Output ("Router: {0}" -f $result.router_host)
Write-Output ("Transport: {0}" -f $result.transport)
if ($result.session_id) {
    Write-Output ("Session: {0}" -f $result.session_id)
}
if ($result.remote_session_dir) {
    Write-Output ("Remote session dir: {0}" -f $result.remote_session_dir)
}
if ($result.state) {
    Write-Output ("State: {0}" -f $result.state)
}
if ($null -ne $result.listen_address) {
    Write-Output ("Listen address: {0}" -f $result.listen_address)
}
if ($null -ne $result.port) {
    Write-Output ("Port: {0}" -f $result.port)
}
if ($null -ne $result.duration_seconds) {
    Write-Output ("Duration seconds: {0}" -f $result.duration_seconds)
}
if ($result.artifact) {
    Write-Output ("Artifact: {0}" -f $result.artifact.local_path)
    if ($result.artifact.local_sha256) {
        Write-Output ("Local SHA256: {0}" -f $result.artifact.local_sha256)
    }
    if ($result.artifact.PSObject.Properties.Name -contains 'remote_sha256' -and $result.artifact.remote_sha256) {
        Write-Output ("Remote SHA256: {0}" -f $result.artifact.remote_sha256)
    }
}
if ($result.raw_text) {
    Write-Output ''
    Write-Output $result.raw_text
}
