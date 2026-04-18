[CmdletBinding()]
param(
    [string]$RouterHost = $env:OPENWRT_ROUTER_HOST,

    [string]$RouterUser = $env:OPENWRT_ROUTER_USER,

    [string]$RouterPassword = $env:OPENWRT_ROUTER_PASSWORD,

    [string]$RouterHostKey = $env:OPENWRT_ROUTER_HOSTKEY,

    [ValidateSet('Auto', 'PuTTY', 'OpenSSH')]
    [string]$Transport = $(if ($env:OPENWRT_ROUTER_TRANSPORT) { $env:OPENWRT_ROUTER_TRANSPORT } else { 'Auto' }),

    [string]$OpenSshKnownHostsFile = $env:OPENWRT_ROUTER_KNOWN_HOSTS_FILE,

    [string]$OpenSshIdentityFile = $env:OPENWRT_ROUTER_IDENTITY_FILE,

    [string]$OutputFile,

    [switch]$IncludePasswallPlan,

    [switch]$AsJson
)

$ErrorActionPreference = 'Stop'

. (Join-Path $PSScriptRoot 'OpenWrtSshTransport.ps1')

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

function Get-RemoteInventoryCommand {
@'
echo '--- system board ---'
ubus call system board
echo '--- openwrt_release ---'
grep -E 'DISTRIB_(ID|RELEASE|ARCH|TARGET|DESCRIPTION)' /etc/openwrt_release
echo '--- os-release ---'
grep -E 'OPENWRT_ARCH|NAME|VERSION' /usr/lib/os-release 2>/dev/null
echo '--- package manager ---'
opkg --version 2>/dev/null || true
apk --version 2>/dev/null || true
echo '--- architectures ---'
opkg print-architecture 2>/dev/null || true
uname -m
echo '--- installed core packages ---'
opkg list-installed 2>/dev/null | grep -E '^(luci-app-passwall2|passwall2|xray-core|xray|sing-box|hysteria|geoview|v2ray-geoip|v2ray-geosite|dnsmasq|dnsmasq-full|firewall4|nftables|luci|dropbear|openssh)' || true
apk list -I 2>/dev/null | grep -E 'passwall|xray|sing-box|hysteria|geoview|v2ray-geo|dnsmasq|firewall4' || true
echo '--- passwall safe status ---'
uci get passwall2.@global[0].enabled 2>/dev/null || true
uci get passwall2.@global[0].node 2>/dev/null || true
echo -n 'nodes_count='; uci show passwall2 2>/dev/null | grep '=nodes' | wc -l
echo -n 'subscriptions_count='; uci show passwall2 2>/dev/null | grep '=subscribe_list' | wc -l
echo '--- binary versions ---'
xray version 2>/dev/null | head -n 2 || true
sing-box version 2>/dev/null | head -n 2 || true
hysteria version 2>/dev/null | head -n 3 || true
geoview -version 2>/dev/null | head -n 1 || true
echo '--- processes ---'
ps w | grep -E '[p]asswall|[x]ray|[s]ing-box|[h]ysteria' || true
echo '--- firewall dnsmasq ---'
fw4 -V 2>/dev/null || true
dnsmasq -v 2>/dev/null | head -n 5 || true
echo '--- resources ---'
free -m 2>/dev/null || true
df -h /overlay /tmp 2>/dev/null || true
echo '--- upgrade tools ---'
which sysupgrade 2>/dev/null || true
sysupgrade -h 2>&1 | head -n 15 || true
echo '--- backup scope ---'
sysupgrade -l 2>/dev/null | grep -E '^/etc/config/(passwall2|passwall2_server|network|firewall|wireless)$|^/etc/dropbear/|^/etc/config/uhttpd$' || true
echo '--- boot partitions ---'
cat /proc/mtd 2>/dev/null || true
cat /proc/cmdline 2>/dev/null || true
mount | grep -E 'overlay|ubifs|squashfs|tmpfs' || true
echo '--- env tools ---'
which fw_printenv 2>/dev/null || true
fw_printenv 2>/dev/null | grep -E 'boot|flag|rootfs|slot' || true
'@
}

function Invoke-ReadOnlyInventory {
    param(
        [psobject]$TransportSpec,
        [string]$RouterHost,
        [string]$RouterUser,
        [string]$RouterPassword,
        [string]$RouterHostKey,
        [string]$RemoteCommand
    )

    $response = Invoke-OpenWrtRemoteCommand -TransportSpec $TransportSpec -RouterHost $RouterHost -RouterUser $RouterUser -RouterPassword $RouterPassword -RouterHostKey $RouterHostKey -CommandText $RemoteCommand -ViaStdinSh
    if ($response.exitCode -ne 0) {
        throw ($response.text + [Environment]::NewLine + "Remote inventory command failed with exit code $($response.exitCode).")
    }

    return $response.output
}

function Get-PasswallPlan {
    param([string]$InventoryFilePath)

    $resolver = Join-Path $PSScriptRoot 'Resolve-Passwall2RouterPlan.ps1'
    if (-not (Test-Path -LiteralPath $resolver)) {
        return $null
    }

    $json = & $resolver -InputFile $InventoryFilePath -AsJson
    if (-not $json) {
        return $null
    }

    return $json | ConvertFrom-Json
}

$RouterHost = Get-RequiredValue -Value $RouterHost -Name 'RouterHost'
$RouterUser = Get-RequiredValue -Value $RouterUser -Name 'RouterUser'
$transportSpec = Resolve-OpenWrtTransportSpec -Transport $Transport -RouterPassword $RouterPassword -RouterHostKey $RouterHostKey -OpenSshKnownHostsFile $OpenSshKnownHostsFile -OpenSshIdentityFile $OpenSshIdentityFile
if ($transportSpec.mode -eq 'PuTTY') {
    $RouterPassword = Get-RequiredValue -Value $RouterPassword -Name 'RouterPassword'
    $RouterHostKey = Get-RequiredValue -Value $RouterHostKey -Name 'RouterHostKey'
}

$remoteCommand = Get-RemoteInventoryCommand
$inventoryText = Invoke-ReadOnlyInventory -TransportSpec $transportSpec -RouterHost $RouterHost -RouterUser $RouterUser -RouterPassword $RouterPassword -RouterHostKey $RouterHostKey -RemoteCommand $remoteCommand
$collectedAt = (Get-Date).ToString('s')

$savedFile = $null
if ($OutputFile) {
    $resolvedOutput = Resolve-Path -LiteralPath (Split-Path -Parent $OutputFile) -ErrorAction SilentlyContinue
    if (-not $resolvedOutput -and (Split-Path -Parent $OutputFile)) {
        $null = New-Item -ItemType Directory -Path (Split-Path -Parent $OutputFile) -Force
    }

    Set-Content -LiteralPath $OutputFile -Value $inventoryText
    $savedFile = (Resolve-Path -LiteralPath $OutputFile).Path
}

$plan = $null
if ($IncludePasswallPlan) {
    $planInput = $savedFile
    if (-not $planInput) {
        $tempFile = Join-Path ([System.IO.Path]::GetTempPath()) ("openwrt-router-inventory-{0}.txt" -f ([guid]::NewGuid().ToString('N')))
        Set-Content -LiteralPath $tempFile -Value $inventoryText
        $planInput = $tempFile
    }

    try {
        $plan = Get-PasswallPlan -InventoryFilePath $planInput
    }
    finally {
        if (-not $savedFile -and $planInput -and (Test-Path -LiteralPath $planInput)) {
            Remove-Item -LiteralPath $planInput -Force
        }
    }
}

$result = [pscustomobject]@{
    collected_at = $collectedAt
    host = $RouterHost
    user = $RouterUser
    host_key = if ($transportSpec.mode -eq 'PuTTY') { $RouterHostKey } else { $null }
    transport = $transportSpec.mode
    openssh_known_hosts_file = if ($transportSpec.mode -eq 'OpenSSH') { $transportSpec.knownHostsFile } else { $null }
    inventory_profile = 'read-only'
    output_file = $savedFile
    raw_text = $inventoryText
    passwall_plan = $plan
}

if ($AsJson) {
    $result | ConvertTo-Json -Depth 8
    exit 0
}

Write-Output 'OpenWrt Router Inventory'
Write-Output '========================'
Write-Output ("Collected at: {0}" -f $result.collected_at)
Write-Output ("Host: {0}" -f $result.host)
Write-Output ("Inventory mode: {0}" -f $result.inventory_profile)
if ($savedFile) {
    Write-Output ("Saved raw output: {0}" -f $savedFile)
}
Write-Output ''
Write-Output $result.raw_text

if ($plan) {
    Write-Output ''
    Write-Output 'PassWall2 plan summary:'
    Write-Output ("- Package manager: {0}" -f $plan.detection.recommended_package_manager)
    Write-Output ("- Architecture: {0}" -f $plan.detection.architecture)
    Write-Output ("- App artifact: {0}" -f $plan.recommendation.app_artifact_name)
    Write-Output ("- Component bundle: {0}" -f $plan.recommendation.component_bundle_name)
}
