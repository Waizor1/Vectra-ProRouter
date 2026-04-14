[CmdletBinding()]
param(
    [string]$RouterHost,

    [string]$RouterUser,

    [string]$RouterPassword,

    [string]$RouterHostKey,

    [string]$FeedChannel = 'stable',

    [switch]$Apply,

    [switch]$AsJson
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$MandatoryBaselinePackages = @(
    'vectra-controller-agent',
    'luci-app-vectra-controller',
    'luci-app-passwall2',
    'xray-core',
    'geoview'
)

$OptionalBaselinePackages = @(
    'sing-box',
    'hysteria'
)

function Get-RequiredValue {
    param(
        [AllowNull()]
        [AllowEmptyString()]
        [string]$Value,
        [string]$Name
    )

    if ([string]::IsNullOrWhiteSpace($Value)) {
        throw "Missing required value: $Name."
    }

    return $Value
}

function Get-PlinkPath {
    $command = Get-Command plink.exe -ErrorAction SilentlyContinue | Select-Object -First 1
    if (-not $command) {
        throw 'plink.exe was not found. Install PuTTY or add plink.exe to PATH.'
    }

    return $command.Source
}

function Read-LocalRegistry {
    $readerPath = Join-Path $PSScriptRoot '..\ProRouter\98 Local\Read-VectraLocalAccess.ps1'
    $resolvedPath = (Resolve-Path -LiteralPath $readerPath).Path
    return & $resolvedPath -AsObject
}

function Get-RegistryValue {
    param(
        [Parameter(Mandatory = $true)]
        [psobject]$Object,
        [Parameter(Mandatory = $true)]
        [string]$PropertyName
    )

    $property = $Object.PSObject.Properties[$PropertyName]
    if (-not $property) {
        return $null
    }

    return $property.Value
}

function Get-RegistryPackageVersion {
    param(
        [Parameter(Mandatory = $true)]
        [psobject]$Versions,
        [Parameter(Mandatory = $true)]
        [string]$PackageName
    )

    $directValue = Get-RegistryValue -Object $Versions -PropertyName $PackageName
    if (-not [string]::IsNullOrWhiteSpace($directValue)) {
        return $directValue
    }

    $normalizedPropertyName = $PackageName.Replace('-', '_')
    return Get-RegistryValue -Object $Versions -PropertyName $normalizedPropertyName
}

function Normalize-OpenWrtTrack {
    param([string]$ReleaseValue)

    if ([string]::IsNullOrWhiteSpace($ReleaseValue)) {
        return $null
    }

    $match = [regex]::Match($ReleaseValue, '^(?<major>\d{2}\.\d{2})')
    if ($match.Success) {
        return $match.Groups['major'].Value
    }

    return $ReleaseValue.Trim()
}

function Get-ExpectedBaseline {
    param([psobject]$Registry)

    $versions = Get-RegistryValue -Object $Registry -PropertyName 'live_versions'
    if (-not $versions) {
        throw 'live_versions is missing in the local private registry.'
    }

    $packages = New-Object System.Collections.Generic.List[object]
    foreach ($packageName in $MandatoryBaselinePackages + $OptionalBaselinePackages) {
        $version = Get-RegistryPackageVersion -Versions $versions -PackageName $packageName
        if ([string]::IsNullOrWhiteSpace($version)) {
            if ($MandatoryBaselinePackages -contains $packageName) {
                throw "Baseline version for $packageName is missing in the local private registry."
            }

            continue
        }

        [void]$packages.Add([pscustomobject]@{
            name = $packageName
            version = $version.Trim()
            pinned = $true
            source = if ($packageName -like 'vectra-*' -or $packageName -like 'luci-app-vectra-*') {
                'vectra-feed'
            } else {
                'router-opkg-feeds'
            }
        })
    }

    $router = Get-RegistryValue -Object $Registry -PropertyName 'router'
    if (-not $router) {
        throw 'router profile is missing in the local private registry.'
    }

    $openwrtTrack = Normalize-OpenWrtTrack -ReleaseValue (Get-RegistryValue -Object $router -PropertyName 'openwrt')

    return [pscustomobject]@{
        board = (Get-RegistryValue -Object $router -PropertyName 'board')
        target = (Get-RegistryValue -Object $router -PropertyName 'target')
        architecture = (Get-RegistryValue -Object $router -PropertyName 'arch')
        openwrtTrack = $openwrtTrack
        layoutFamily = (Get-RegistryValue -Object $router -PropertyName 'layout_family')
        packages = $packages.ToArray()
    }
}

function Resolve-RouterAccess {
    param([psobject]$Registry)

    $router = Get-RegistryValue -Object $Registry -PropertyName 'router'
    if (-not $router) {
        throw 'router access is missing in the local private registry.'
    }

    return [pscustomobject]@{
        host = if ($RouterHost) { $RouterHost } else { Get-RegistryValue -Object $router -PropertyName 'host' }
        user = if ($RouterUser) { $RouterUser } else { Get-RegistryValue -Object $router -PropertyName 'user' }
        password = if ($RouterPassword) { $RouterPassword } else { Get-RegistryValue -Object $router -PropertyName 'password' }
        hostKey = if ($RouterHostKey) { $RouterHostKey } else { Get-RegistryValue -Object $router -PropertyName 'host_key_sha256' }
    }
}

function Get-VectraFeedUrls {
    param(
        [psobject]$Registry,
        [psobject]$ExpectedBaseline,
        [string]$Channel
    )

    $domains = Get-RegistryValue -Object $Registry -PropertyName 'domains'
    if (-not $domains) {
        throw 'domains is missing in the local private registry.'
    }

    $artifactBaseUrl = Get-RegistryValue -Object $domains -PropertyName 'artifacts'
    if ([string]::IsNullOrWhiteSpace($artifactBaseUrl)) {
        $artifactBaseUrl = Get-RegistryValue -Object $domains -PropertyName 'api'
    }
    if ([string]::IsNullOrWhiteSpace($artifactBaseUrl)) {
        $artifactBaseUrl = Get-RegistryValue -Object $domains -PropertyName 'router_api'
    }
    if ([string]::IsNullOrWhiteSpace($artifactBaseUrl)) {
        throw 'domains.artifacts/router_api is missing in the local private registry.'
    }

    $artifactBaseUrl = $artifactBaseUrl.TrimEnd('/')
    if ($artifactBaseUrl -match '/artifacts$') {
        $feedBaseUrl = ('{0}/openwrt/{1}/{2}' -f $artifactBaseUrl, $Channel.Trim(), $ExpectedBaseline.architecture)
    } else {
        $feedBaseUrl = ('{0}/artifacts/openwrt/{1}/{2}' -f $artifactBaseUrl, $Channel.Trim(), $ExpectedBaseline.architecture)
    }

    return [pscustomobject]@{
        feedName = 'vectra'
        feedUrl = $feedBaseUrl
        feedFile = '/etc/opkg/customfeeds.conf.d/vectra.conf'
        publicKeyUrl = "$feedBaseUrl/vectra.pub"
        indexUrl = "$feedBaseUrl/index.json"
        packagesUrl = "$feedBaseUrl/Packages"
        signatureUrl = "$feedBaseUrl/Packages.sig"
    }
}

function Test-RemoteOpenWrtFeed {
    param(
        [psobject]$FeedInfo,
        [psobject]$ExpectedBaseline
    )

    $index = Invoke-RestMethod -Method Get -Uri $FeedInfo.indexUrl
    if (-not $index) {
        throw "Unable to read Vectra feed index: $($FeedInfo.indexUrl)"
    }

    $feedPackages = @($index.packages)
    foreach ($packageName in @('vectra-controller-agent', 'luci-app-vectra-controller')) {
        $version = ($ExpectedBaseline.packages | Where-Object { $_.name -eq $packageName } | Select-Object -ExpandProperty version -First 1)
        $expectedFile = if ($packageName -eq 'luci-app-vectra-controller') {
            '{0}_{1}_all.ipk' -f $packageName, $version
        } else {
            '{0}_{1}_{2}.ipk' -f $packageName, $version, $ExpectedBaseline.architecture
        }

        if ($feedPackages -notcontains $expectedFile) {
            throw "Vectra feed does not contain expected package $expectedFile."
        }
    }

    return [pscustomobject]@{
        feedName = $index.feedName
        channel = $index.channel
        targetArch = $index.targetArch
        packages = $feedPackages
    }
}

function Get-RemotePreflightCommand {
@'
set -eu

read_release_value() {
    local key="$1"
    grep -E "^${key}=" /etc/openwrt_release 2>/dev/null | head -n 1 | cut -d= -f2- | tr -d "'" | tr -d '"'
}

echo "BOARD_NAME=$(ubus call system board 2>/dev/null | jsonfilter -e '@.board_name' 2>/dev/null || true)"
echo "TARGET=$(read_release_value DISTRIB_TARGET)"
echo "ARCHITECTURE=$(read_release_value DISTRIB_ARCH)"
echo "OPENWRT_RELEASE=$(read_release_value DISTRIB_RELEASE)"
echo "KERNEL_FIRMWARE_SLOT=$(grep -o 'firmware=[^ ]*' /proc/cmdline 2>/dev/null | head -n 1 | cut -d= -f2 || true)"
echo "FW_BOOTMENU_1=$(fw_printenv -n bootmenu_1 2>/dev/null || true)"
echo "FW_BOOTMENU_2=$(fw_printenv -n bootmenu_2 2>/dev/null || true)"
echo "VECTRA_STATE_DIR=$(if [ -d /etc/vectra-controller ]; then echo present; else echo missing; fi)"
echo "VECTRA_FEED_LINE=$(grep -Rhs '^src/gz[[:space:]]\+vectra[[:space:]]' /etc/opkg/customfeeds.conf /etc/opkg/customfeeds.conf.d/*.conf 2>/dev/null | head -n 1 || true)"
echo "CUSTOM_FEED_DIR=$(if [ -d /etc/opkg/customfeeds.conf.d ]; then echo present; else echo missing; fi)"
'@
}

function Invoke-RemoteCommand {
    param(
        [string]$PlinkPath,
        [psobject]$RouterAccess,
        [string]$RemoteCommand
    )

    $plinkArgs = @(
        '-ssh',
        '-batch',
        '-no-antispoof',
        '-hostkey', $RouterAccess.hostKey,
        '-l', $RouterAccess.user,
        '-pw', $RouterAccess.password,
        $RouterAccess.host,
        'sh -s'
    )

    $output = $RemoteCommand | & $PlinkPath @plinkArgs 2>&1
    return [pscustomobject]@{
        exitCode = $LASTEXITCODE
        output = @($output)
        text = (@($output) -join "`n").Trim()
    }
}

function Convert-RemotePairs {
    param([string[]]$Lines)

    $result = [ordered]@{}
    foreach ($line in $Lines) {
        if ($line -notmatch '=') {
            continue
        }

        $pair = $line -split '=', 2
        if ($pair.Count -ne 2) {
            continue
        }

        $result[$pair[0]] = $pair[1]
    }

    return [pscustomobject]$result
}

function Assert-CertifiedRouterProfile {
    param(
        [psobject]$ExpectedBaseline,
        [psobject]$RemoteFacts
    )

    if ($RemoteFacts.BOARD_NAME -ne $ExpectedBaseline.board) {
        throw "Router board mismatch: got '$($RemoteFacts.BOARD_NAME)', expected '$($ExpectedBaseline.board)'."
    }

    if ($RemoteFacts.TARGET -ne $ExpectedBaseline.target) {
        throw "Router target mismatch: got '$($RemoteFacts.TARGET)', expected '$($ExpectedBaseline.target)'."
    }

    if ($RemoteFacts.ARCHITECTURE -ne $ExpectedBaseline.architecture) {
        throw "Router architecture mismatch: got '$($RemoteFacts.ARCHITECTURE)', expected '$($ExpectedBaseline.architecture)'."
    }

    $remoteTrack = Normalize-OpenWrtTrack -ReleaseValue $RemoteFacts.OPENWRT_RELEASE
    if ($remoteTrack -ne $ExpectedBaseline.openwrtTrack) {
        throw "Router OpenWrt track mismatch: got '$($RemoteFacts.OPENWRT_RELEASE)', expected '$($ExpectedBaseline.openwrtTrack).x'."
    }

    $hasStockBootMenu =
        ($RemoteFacts.FW_BOOTMENU_1 -like 'Startup firmware0*') -and
        ($RemoteFacts.FW_BOOTMENU_2 -like 'Startup firmware1*')
    if ($ExpectedBaseline.layoutFamily -eq 'stock-layout' -and -not $hasStockBootMenu) {
        throw 'Router did not prove the expected AX3000T stock-layout boot environment.'
    }

    if ($RemoteFacts.VECTRA_STATE_DIR -ne 'present') {
        throw '/etc/vectra-controller is missing, so controller identity persistence cannot be trusted for post-sysupgrade restore.'
    }
}

function ConvertTo-PosixSingleQuoted {
    param([string]$Value)

    if ($null -eq $Value) {
        return "''"
    }

    $replacement = "'" + '"' + "'" + '"' + "'"
    return "'" + $Value.Replace("'", $replacement) + "'"
}

function New-FeedEnsureCommand {
    param([psobject]$FeedInfo)

    $feedLine = 'src/gz {0} {1}' -f $FeedInfo.feedName, $FeedInfo.feedUrl
    $feedLineQuoted = ConvertTo-PosixSingleQuoted -Value $feedLine
    $feedFileQuoted = ConvertTo-PosixSingleQuoted -Value $FeedInfo.feedFile
    $publicKeyUrlQuoted = ConvertTo-PosixSingleQuoted -Value $FeedInfo.publicKeyUrl

@"
set -eu
mkdir -p /etc/opkg/customfeeds.conf.d
printf '%s\n' $feedLineQuoted > $feedFileQuoted
wget -qO /tmp/vectra-feed.pub $publicKeyUrlQuoted
opkg-key add /tmp/vectra-feed.pub
rm -f /tmp/vectra-feed.pub
"@
}

function New-OpkgAvailabilityCommand {
    param([object[]]$Packages)

    $checks = foreach ($package in $Packages) {
        $packageName = ConvertTo-PosixSingleQuoted -Value $package.name
        $packageVersion = ConvertTo-PosixSingleQuoted -Value $package.version
@"
if ! opkg info $packageName 2>/dev/null | grep -F "Version: $($package.version)" >/dev/null; then
    echo "MISSING_PACKAGE=$($package.name)@$($package.version)"
    exit 42
fi
"@
    }

@"
set -eu
opkg update
$(($checks -join "`n"))
"@
}

function New-InstalledBaselineStatusCommand {
    param([object[]]$Packages)

    $checks = foreach ($package in $Packages) {
        $packageName = ConvertTo-PosixSingleQuoted -Value $package.name
@"
installed_line=`$(opkg list-installed $packageName 2>/dev/null | head -n 1 || true)
echo "INSTALLED_$($package.name)=`$installed_line"
"@
    }

@"
set -eu
$(($checks -join "`n"))
"@
}

function New-OpkgInstallCommand {
    param([object[]]$Packages)

    $installArgs = ($Packages | ForEach-Object { '{0}={1}' -f $_.name, $_.version }) -join ' '
    $verifyCommands = foreach ($package in $Packages) {
@"
if ! opkg list-installed $($package.name) 2>/dev/null | grep -F "$($package.name) - $($package.version)" >/dev/null; then
    echo "VERIFY_FAILED=$($package.name)@$($package.version)"
    exit 43
fi
"@
    }

@"
set -eu
opkg install --force-reinstall $installArgs
$(($verifyCommands -join "`n"))
"@
}

function Sanitize-CommandOutput {
    param([string]$Text)

    if ([string]::IsNullOrWhiteSpace($Text)) {
        return ''
    }

    return ($Text -split "`r?`n" | Where-Object {
        $_ -and
        $_ -notmatch 'password' -and
        $_ -notmatch 'token' -and
        $_ -notmatch 'authorization'
    }) -join "`n"
}

$registry = Read-LocalRegistry
$routerAccess = Resolve-RouterAccess -Registry $registry
$routerAccess.host = Get-RequiredValue -Value $routerAccess.host -Name 'RouterHost'
$routerAccess.user = Get-RequiredValue -Value $routerAccess.user -Name 'RouterUser'
$routerAccess.password = Get-RequiredValue -Value $routerAccess.password -Name 'RouterPassword'
$routerAccess.hostKey = Get-RequiredValue -Value $routerAccess.hostKey -Name 'RouterHostKey'

$expectedBaseline = Get-ExpectedBaseline -Registry $registry
$feedInfo = Get-VectraFeedUrls -Registry $registry -ExpectedBaseline $expectedBaseline -Channel $FeedChannel
$feedIndex = Test-RemoteOpenWrtFeed -FeedInfo $feedInfo -ExpectedBaseline $expectedBaseline
$plinkPath = Get-PlinkPath

$preflight = Invoke-RemoteCommand -PlinkPath $plinkPath -RouterAccess $routerAccess -RemoteCommand (Get-RemotePreflightCommand)
if ($preflight.exitCode -ne 0) {
    throw "Router preflight collection failed with exit code $($preflight.exitCode): $(Sanitize-CommandOutput -Text $preflight.text)"
}

$remoteFacts = Convert-RemotePairs -Lines $preflight.output
Assert-CertifiedRouterProfile -ExpectedBaseline $expectedBaseline -RemoteFacts $remoteFacts

$writePreview = [ordered]@{
    ensure_feed = 'Write /etc/opkg/customfeeds.conf.d/vectra.conf with pinned Vectra feed URL'
    install_key = 'Download and install Vectra opkg signing key'
    opkg_update = 'Refresh package indexes'
    restore_packages = ($expectedBaseline.packages | ForEach-Object { '{0}={1}' -f $_.name, $_.version }) -join ', '
}

$installedState = Invoke-RemoteCommand -PlinkPath $plinkPath -RouterAccess $routerAccess -RemoteCommand (New-InstalledBaselineStatusCommand -Packages $expectedBaseline.packages)
if ($installedState.exitCode -ne 0) {
    throw "Baseline package status collection failed with exit code $($installedState.exitCode): $(Sanitize-CommandOutput -Text $installedState.text)"
}

$steps = New-Object System.Collections.Generic.List[object]
[void]$steps.Add([pscustomobject]@{
    name = 'preflight'
    mode = 'read-only'
    status = 'ok'
    output = Sanitize-CommandOutput -Text $preflight.text
})
[void]$steps.Add([pscustomobject]@{
    name = 'installed-baseline'
    mode = 'read-only'
    status = 'ok'
    output = Sanitize-CommandOutput -Text $installedState.text
})

if ($Apply) {
    $feedWrite = Invoke-RemoteCommand -PlinkPath $plinkPath -RouterAccess $routerAccess -RemoteCommand (New-FeedEnsureCommand -FeedInfo $feedInfo)
    if ($feedWrite.exitCode -ne 0) {
        throw "Feed/key restore failed with exit code $($feedWrite.exitCode): $(Sanitize-CommandOutput -Text $feedWrite.text)"
    }
    [void]$steps.Add([pscustomobject]@{
        name = 'feed-restore'
        mode = 'write'
        status = 'ok'
        output = Sanitize-CommandOutput -Text $feedWrite.text
    })

    $availability = Invoke-RemoteCommand -PlinkPath $plinkPath -RouterAccess $routerAccess -RemoteCommand (New-OpkgAvailabilityCommand -Packages $expectedBaseline.packages)
    if ($availability.exitCode -ne 0) {
        throw "Baseline package availability check failed with exit code $($availability.exitCode): $(Sanitize-CommandOutput -Text $availability.text)"
    }
    [void]$steps.Add([pscustomobject]@{
        name = 'baseline-availability'
        mode = 'write'
        status = 'ok'
        output = Sanitize-CommandOutput -Text $availability.text
    })

    $install = Invoke-RemoteCommand -PlinkPath $plinkPath -RouterAccess $routerAccess -RemoteCommand (New-OpkgInstallCommand -Packages $expectedBaseline.packages)
    if ($install.exitCode -ne 0) {
        throw "Package restore failed with exit code $($install.exitCode): $(Sanitize-CommandOutput -Text $install.text)"
    }
    [void]$steps.Add([pscustomobject]@{
        name = 'package-restore'
        mode = 'write'
        status = 'ok'
        output = Sanitize-CommandOutput -Text $install.text
    })
}

$result = [pscustomobject]@{
    mode = if ($Apply) { 'apply' } else { 'dry-run' }
    router = [pscustomobject]@{
        host = $routerAccess.host
        user = $routerAccess.user
        board = $remoteFacts.BOARD_NAME
        target = $remoteFacts.TARGET
        architecture = $remoteFacts.ARCHITECTURE
        openwrtRelease = $remoteFacts.OPENWRT_RELEASE
        layoutFamily = $expectedBaseline.layoutFamily
        firmwareSlot = $remoteFacts.KERNEL_FIRMWARE_SLOT
    }
    feed = [pscustomobject]@{
        name = $feedInfo.feedName
        url = $feedInfo.feedUrl
        file = $feedInfo.feedFile
        publicKeyUrl = $feedInfo.publicKeyUrl
        index = $feedIndex
    }
    baseline = $expectedBaseline.packages
    write_preview = [pscustomobject]$writePreview
    steps = $steps.ToArray()
}

if ($AsJson) {
    $result | ConvertTo-Json -Depth 8
    exit 0
}

Write-Output 'Vectra Post-Sysupgrade Restore'
Write-Output '=============================='
Write-Output ("Mode: {0}" -f $result.mode)
Write-Output ("Router: {0} ({1})" -f $result.router.host, $result.router.board)
Write-Output ("OpenWrt: {0} / {1} / {2}" -f $result.router.openwrtRelease, $result.router.target, $result.router.architecture)
Write-Output ("Layout: {0}" -f $result.router.layoutFamily)
Write-Output ("Vectra feed: {0}" -f $result.feed.url)
Write-Output ''
Write-Output 'Pinned baseline packages:'
foreach ($package in $result.baseline) {
    Write-Output ("- {0} {1} ({2})" -f $package.name, $package.version, $package.source)
}
Write-Output ''
if (-not $Apply) {
    Write-Output 'Dry-run only. Planned write steps:'
    foreach ($entry in $result.write_preview.PSObject.Properties) {
        Write-Output ("- {0}: {1}" -f $entry.Name, $entry.Value)
    }
    Write-Output ''
    Write-Output 'Use -Apply only during a short LAN-attended maintenance window.'
} else {
    Write-Output 'Applied steps:'
    foreach ($step in $result.steps | Where-Object { $_.mode -eq 'write' }) {
        Write-Output ("- {0}: {1}" -f $step.name, $step.status)
    }
}
