[CmdletBinding(DefaultParameterSetName = 'Auto')]
param(
    [Parameter(ParameterSetName = 'File', Mandatory = $true)]
    [string]$InputFile,

    [Parameter(ParameterSetName = 'Text', Mandatory = $true)]
    [string]$RawText,

    [ValidateSet('passwall2')]
    [string]$App = 'passwall2',

    [switch]$SkipReleaseLookup,

    [switch]$AsJson
)

$ErrorActionPreference = 'Stop'

function Get-InputText {
    param(
        [string]$Path,
        [string]$InlineText
    )

    if ($Path) {
        return Get-Content -LiteralPath $Path -Raw
    }

    if ($InlineText) {
        return $InlineText
    }

    if ([Console]::IsInputRedirected) {
        return [Console]::In.ReadToEnd()
    }

    throw "Provide -InputFile, -RawText, or pipe router output into stdin."
}

function Get-FirstRegexValue {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Text,

        [Parameter(Mandatory = $true)]
        [string[]]$Patterns
    )

    foreach ($pattern in $Patterns) {
        $match = [regex]::Match($Text, $pattern, [System.Text.RegularExpressions.RegexOptions]::IgnoreCase -bor [System.Text.RegularExpressions.RegexOptions]::Multiline -bor [System.Text.RegularExpressions.RegexOptions]::Singleline)
        if ($match.Success) {
            return $match.Groups[1].Value.Trim()
        }
    }

    return $null
}

function Get-UniqueList {
    param([string[]]$Items)

    $list = New-Object System.Collections.Generic.List[string]
    foreach ($item in $Items) {
        if ([string]::IsNullOrWhiteSpace($item)) {
            continue
        }

        if (-not $list.Contains($item)) {
            [void]$list.Add($item)
        }
    }

    return @($list)
}

function Get-DetectedPackageManagers {
    param([string]$Text)

    $detected = New-Object System.Collections.Generic.List[string]

    if ($Text -match '(?im)^\s*opkg\s+version\b' -or $Text -match '(?im)^\s*arch\s+[A-Za-z0-9_.-]+\s+\d+\s*$') {
        [void]$detected.Add('opkg')
    }

    if ($Text -match '(?im)^\s*apk(?:-tools)?\s+\d' -or $Text -match '(?im)^\s*installed:\s*apk-tools\b') {
        [void]$detected.Add('apk')
    }

    return @(Get-UniqueList -Items $detected)
}

function Get-PrimaryOpkgArchitecture {
    param([string]$Text)

    $matches = [regex]::Matches($Text, '(?im)^\s*arch\s+([A-Za-z0-9_.-]+)\s+(\d+)\s*$')
    if ($matches.Count -eq 0) {
        return $null
    }

    $rows = foreach ($match in $matches) {
        $archName = $match.Groups[1].Value
        if ($archName -in @('all', 'noarch')) {
            continue
        }

        [pscustomobject]@{
            name = $archName
            priority = [int]$match.Groups[2].Value
        }
    }

    $primary = $rows | Sort-Object -Property priority -Descending | Select-Object -First 1
    if ($primary) {
        return $primary.name
    }

    return $null
}

function Resolve-Architecture {
    param(
        [string]$DistribArch,
        [string]$OpenWrtArch,
        [string]$OpkgPrimaryArch,
        [string]$UnameMachine,
        [string]$Target
    )

    if ($DistribArch) {
        return [pscustomobject]@{
            arch = $DistribArch
            source = 'DISTRIB_ARCH'
            confidence = 'high'
        }
    }

    if ($OpenWrtArch) {
        return [pscustomobject]@{
            arch = $OpenWrtArch
            source = 'OPENWRT_ARCH'
            confidence = 'high'
        }
    }

    if ($OpkgPrimaryArch) {
        return [pscustomobject]@{
            arch = $OpkgPrimaryArch
            source = 'opkg print-architecture'
            confidence = 'medium'
        }
    }

    if ($UnameMachine -match '^aarch64$' -and $Target -match 'filogic') {
        return [pscustomobject]@{
            arch = 'aarch64_cortex-a53'
            source = 'target+uname heuristic'
            confidence = 'low'
        }
    }

    if ($UnameMachine) {
        return [pscustomobject]@{
            arch = $UnameMachine
            source = 'uname -m heuristic'
            confidence = 'low'
        }
    }

    return [pscustomobject]@{
        arch = $null
        source = $null
        confidence = 'unknown'
    }
}

function Resolve-OpenWrtPolicy {
    param(
        [string]$ReleaseVersion,
        [string[]]$DetectedPackageManagers
    )

    $manager = $null
    $format = $null
    $basis = $null
    $notes = New-Object System.Collections.Generic.List[string]

    $versionMatch = [regex]::Match($ReleaseVersion, '(?<!\d)(\d{2})\.(\d{1,2})')
    if ($versionMatch.Success) {
        $major = [int]$versionMatch.Groups[1].Value
        $minor = [int]$versionMatch.Groups[2].Value

        if ($major -lt 25 -or ($major -eq 25 -and $minor -lt 12)) {
            $manager = 'opkg'
            $format = 'ipk'
            $basis = "OpenWrt $ReleaseVersion policy: prefer opkg/.ipk before 25.12"
        }
        else {
            $manager = 'apk'
            $format = 'apk'
            $basis = "OpenWrt $ReleaseVersion policy: prefer apk/.apk on 25.12+"
        }
    }
    elseif ($DetectedPackageManagers.Count -eq 1) {
        $manager = $DetectedPackageManagers[0]
        $format = if ($manager -eq 'apk') { 'apk' } else { 'ipk' }
        $basis = "Detected package manager output only: $manager"
    }
    elseif ($DetectedPackageManagers -contains 'opkg') {
        $manager = 'opkg'
        $format = 'ipk'
        $basis = 'opkg detected without a parseable OpenWrt release'
    }
    elseif ($DetectedPackageManagers -contains 'apk') {
        $manager = 'apk'
        $format = 'apk'
        $basis = 'apk detected without a parseable OpenWrt release'
    }
    else {
        $manager = 'opkg'
        $format = 'ipk'
        $basis = 'workspace default fallback: OpenWrt 24.xx path'
        [void]$notes.Add('Package manager was not detected from pasted output; defaulted to the workspace 24.xx policy.')
    }

    if ($DetectedPackageManagers.Count -gt 0 -and -not ($DetectedPackageManagers -contains $manager)) {
        [void]$notes.Add("Detected package manager output conflicts with the release-based policy. Re-check pasted router facts before installing packages.")
    }

    return [pscustomobject]@{
        manager = $manager
        package_format = $format
        basis = $basis
        notes = @($notes)
    }
}

function Get-PackageVersion {
    param(
        [string]$Text,
        [string[]]$Names
    )

    foreach ($name in $Names) {
        $patterns = @(
            "(?im)^\s*$([regex]::Escape($name))\s*-\s*([^\s]+)\s*$",
            "(?im)^\s*$([regex]::Escape($name))\s+([^\s]+)\s+"
        )

        foreach ($pattern in $patterns) {
            $match = [regex]::Match($Text, $pattern)
            if ($match.Success) {
                return $match.Groups[1].Value.Trim()
            }
        }
    }

    return $null
}

function Get-BinaryVersion {
    param(
        [string]$Text,
        [string[]]$Patterns
    )

    foreach ($pattern in $Patterns) {
        $match = [regex]::Match($Text, $pattern)
        if ($match.Success) {
            return $match.Groups[1].Value.Trim()
        }
    }

    return $null
}

function Get-NormalizedPackageBaseVersion {
    param([string]$Version)

    if ([string]::IsNullOrWhiteSpace($Version)) {
        return $null
    }

    return ($Version -replace '-r\d+$', '').Trim()
}

function Get-CommandExamples {
    param(
        [string]$PackageManager,
        [string]$AppArtifactName,
        [string]$BundleArtifactName
    )

    $commands = New-Object System.Collections.Generic.List[string]

    if ($PackageManager -eq 'opkg') {
        if ($AppArtifactName) {
            [void]$commands.Add("opkg install ./$AppArtifactName")
        }
        else {
            [void]$commands.Add('opkg install ./luci-app-passwall2_<version>_all.ipk')
        }

        if ($BundleArtifactName) {
            [void]$commands.Add("# unzip $BundleArtifactName and install only the component packages you need with opkg")
        }
    }
    elseif ($PackageManager -eq 'apk') {
        if ($AppArtifactName) {
            [void]$commands.Add("apk add ./$AppArtifactName")
        }
        else {
            [void]$commands.Add('apk add ./luci-app-passwall2_<version>.apk')
        }

        if ($BundleArtifactName) {
            [void]$commands.Add("# unpack $BundleArtifactName and add the selected component packages with apk")
        }
    }

    [void]$commands.Add('/etc/init.d/passwall2 restart')
    [void]$commands.Add('lua /usr/share/passwall2/rule_update.lua log geoip,geosite')
    [void]$commands.Add('lua /usr/share/passwall2/subscribe.lua start all')

    return @($commands)
}

$text = Get-InputText -Path $InputFile -InlineText $RawText

$model = Get-FirstRegexValue -Text $text -Patterns @(
    '(?im)^\s*Router model:\s*(.+?)\s*$',
    '"model"\s*:\s*"([^"]+)"'
)

$boardName = Get-FirstRegexValue -Text $text -Patterns @(
    '(?im)^\s*board_name\s*:\s*(.+?)\s*$',
    '"board_name"\s*:\s*"([^"]+)"'
)

$system = Get-FirstRegexValue -Text $text -Patterns @(
    '(?im)^\s*SoC:\s*(.+?)\s*$',
    '(?im)^\s*system\s*:\s*(.+?)\s*$',
    '"system"\s*:\s*"([^"]+)"'
)

$releaseVersion = Get-FirstRegexValue -Text $text -Patterns @(
    "(?im)^\s*DISTRIB_RELEASE=['""]?([^'""]+)['""]?\s*$",
    '"version"\s*:\s*"((?:\d{2}\.\d{1,2}(?:\.\d+)?)|SNAPSHOT[^"]*)"'
)

$releaseDescription = Get-FirstRegexValue -Text $text -Patterns @(
    "(?im)^\s*DISTRIB_DESCRIPTION=['""]?([^'""]+)['""]?\s*$",
    '"description"\s*:\s*"([^"]+)"'
)

$target = Get-FirstRegexValue -Text $text -Patterns @(
    "(?im)^\s*DISTRIB_TARGET=['""]?([^'""]+)['""]?\s*$",
    '"target"\s*:\s*"([^"]+)"'
)

$distribArch = Get-FirstRegexValue -Text $text -Patterns @(
    "(?im)^\s*DISTRIB_ARCH=['""]?([^'""]+)['""]?\s*$"
)

$openwrtArch = Get-FirstRegexValue -Text $text -Patterns @(
    "(?im)^\s*OPENWRT_ARCH=['""]?([^'""]+)['""]?\s*$"
)

$unameMachine = Get-FirstRegexValue -Text $text -Patterns @(
    '(?im)^\s*(aarch64)\s*$',
    '(?im)^\s*(armv7l)\s*$',
    '(?im)^\s*(x86_64)\s*$',
    '(?im)^\s*(mipsel_24kc)\s*$'
)

$opkgPrimaryArch = Get-PrimaryOpkgArchitecture -Text $text
$detectedPackageManagers = Get-DetectedPackageManagers -Text $text
$archResolution = Resolve-Architecture -DistribArch $distribArch -OpenWrtArch $openwrtArch -OpkgPrimaryArch $opkgPrimaryArch -UnameMachine $unameMachine -Target $target
$packagePolicy = Resolve-OpenWrtPolicy -ReleaseVersion $releaseVersion -DetectedPackageManagers $detectedPackageManagers

$installedPasswall2 = Get-PackageVersion -Text $text -Names @('luci-app-passwall2')
$installedXray = Get-PackageVersion -Text $text -Names @('xray-core', 'xray')
$installedSingBox = Get-PackageVersion -Text $text -Names @('sing-box')
$installedHysteria = Get-PackageVersion -Text $text -Names @('hysteria', 'hysteria2')
$installedGeoview = Get-PackageVersion -Text $text -Names @('geoview', 'v2ray-geoip', 'v2ray-geosite')

$runtimeXray = Get-BinaryVersion -Text $text -Patterns @(
    '(?im)^Xray\s+([^\s]+)\b'
)
$runtimeSingBox = Get-BinaryVersion -Text $text -Patterns @(
    '(?im)^sing-box version\s+([^\s]+)\b',
    '(?im)^sing-box\s+([^\s]+)\b'
)
$runtimeHysteria = Get-BinaryVersion -Text $text -Patterns @(
    '(?im)^Version:\s*([^\s]+)\s*$',
    '(?im)^hysteria(?:\s+version)?\s+([^\s]+)\b'
)
$runtimeGeoview = Get-BinaryVersion -Text $text -Patterns @(
    '(?im)^Geoview\s+([^\s]+)\b'
)

$notes = New-Object System.Collections.Generic.List[string]
foreach ($note in $packagePolicy.notes) {
    [void]$notes.Add($note)
}

if ($archResolution.confidence -eq 'low') {
    [void]$notes.Add("Architecture was inferred from heuristics ($($archResolution.source)); confirm DISTRIB_ARCH before final package installation.")
}

if (-not $releaseVersion) {
    [void]$notes.Add('OpenWrt release version was not parsed from the pasted output; package-manager recommendation is less reliable.')
}

$versionComparisons = @(
    @{ Name = 'xray'; Package = $installedXray; Runtime = $runtimeXray },
    @{ Name = 'sing-box'; Package = $installedSingBox; Runtime = $runtimeSingBox },
    @{ Name = 'hysteria'; Package = $installedHysteria; Runtime = $runtimeHysteria },
    @{ Name = 'geoview'; Package = $installedGeoview; Runtime = $runtimeGeoview }
)

foreach ($comparison in $versionComparisons) {
    $normalizedPackage = Get-NormalizedPackageBaseVersion -Version $comparison.Package
    if ($normalizedPackage -and $comparison.Runtime -and $normalizedPackage -ne $comparison.Runtime) {
        [void]$notes.Add("Runtime drift detected for $($comparison.Name): package database says $($comparison.Package), but the binary reports $($comparison.Runtime).")
    }
}

if (-not $model -and $boardName) {
    $model = $boardName
}

$releaseLookup = $null
$appAsset = $null
$bundleAsset = $null

if (-not $SkipReleaseLookup) {
    $releaseScript = Join-Path $PSScriptRoot 'Get-Passwall2ReleaseAssets.ps1'
    if (Test-Path -LiteralPath $releaseScript) {
        try {
            $lookupArgs = @{
                App = $App
                PackageManager = $packagePolicy.manager
                AsJson = $true
            }

            if ($archResolution.arch) {
                $lookupArgs.Arch = $archResolution.arch
            }

            $releaseJson = & $releaseScript @lookupArgs
            $releaseLookup = $releaseJson | ConvertFrom-Json

            $appAsset = @($releaseLookup.recommended_assets | Where-Object { $_.name -match '^luci-app-passwall2.*\.(ipk|apk)$' }) | Select-Object -First 1
            $bundleAsset = @($releaseLookup.recommended_assets | Where-Object { $_.name -match '^passwall_packages_' }) | Select-Object -First 1
        }
        catch {
            [void]$notes.Add("Live release lookup failed: $($_.Exception.Message)")
        }
    }
    else {
        [void]$notes.Add('Release lookup helper script was not found; recommended assets were not resolved.')
    }
}
else {
    [void]$notes.Add('Live release lookup was skipped on request.')
}

$appAssetName = if ($appAsset) { $appAsset.name } else { $null }
$appAssetUrl = if ($appAsset) { $appAsset.download_url } else { $null }
$bundleAssetName = if ($bundleAsset) { $bundleAsset.name } else { $null }
$bundleAssetUrl = if ($bundleAsset) { $bundleAsset.download_url } else { $null }

$componentStrategy = if ($packagePolicy.manager -eq 'opkg') {
    'Prefer package-based component updates; use the built-in binary updater only as a fallback/manual override.'
}
elseif ($packagePolicy.manager -eq 'apk') {
    'Prefer package-manager transactions for components; confirm router-side apk workflow before applying.'
}
else {
    'Do not use the built-in updater as the default path until the router package manager is confirmed.'
}

$checklist = @(
    'Back up /etc/config/passwall2 and /etc/config/passwall2_server before touching packages or binaries.',
    'Capture current installed package versions and binary versions before the change window.',
    "Use $($packagePolicy.manager) and .$($packagePolicy.package_format) artifacts for the main PassWall2 application update."
)

if ($appAssetName) {
    $checklist += "Install the app package that matches the current release: $appAssetName."
}
else {
    $checklist += 'Resolve the exact luci-app-passwall2 package for the router before installation.'
}

if ($bundleAssetName) {
    $checklist += "Extract $bundleAssetName and install only the component packages required on this router."
}
else {
    $checklist += 'Resolve the matching component bundle for this architecture before updating xray/sing-box/hysteria/geodata packages.'
}

$checklist += 'Restart PassWall2 after package changes and verify service health before any subscription or rule refresh.'
$checklist += 'Keep application update, component update, geo rules refresh, and subscription refresh as separate maintenance actions.'

$commands = Get-CommandExamples -PackageManager $packagePolicy.manager -AppArtifactName $appAssetName -BundleArtifactName $bundleAssetName

$result = [pscustomobject]@{
    app = $App
    router = [pscustomobject]@{
        model = $model
        board_name = $boardName
        system = $system
        target = $target
        openwrt_release = $releaseVersion
        openwrt_description = $releaseDescription
    }
    detection = [pscustomobject]@{
        package_managers_detected = @($detectedPackageManagers)
        recommended_package_manager = $packagePolicy.manager
        recommended_package_format = $packagePolicy.package_format
        package_manager_basis = $packagePolicy.basis
        architecture = $archResolution.arch
        architecture_source = $archResolution.source
        architecture_confidence = $archResolution.confidence
        candidates = [pscustomobject]@{
            distrib_arch = $distribArch
            openwrt_arch = $openwrtArch
            opkg_primary_arch = $opkgPrimaryArch
            uname_machine = $unameMachine
        }
    }
    installed = [pscustomobject]@{
        package_versions = [pscustomobject]@{
            passwall2 = $installedPasswall2
            xray = $installedXray
            sing_box = $installedSingBox
            hysteria = $installedHysteria
            geoview_or_geodata = $installedGeoview
        }
        runtime_versions = [pscustomobject]@{
            xray = $runtimeXray
            sing_box = $runtimeSingBox
            hysteria = $runtimeHysteria
            geoview = $runtimeGeoview
        }
    }
    recommendation = [pscustomobject]@{
        app_artifact_name = $appAssetName
        app_artifact_url = $appAssetUrl
        component_bundle_name = $bundleAssetName
        component_bundle_url = $bundleAssetUrl
        component_update_strategy = $componentStrategy
        built_in_component_updater = 'fallback-only'
    }
    commands = @($commands)
    checklist = @($checklist)
    notes = @(Get-UniqueList -Items $notes)
    release_lookup = if ($releaseLookup) {
        [pscustomobject]@{
            tag = $releaseLookup.tag
            published_at = $releaseLookup.published_at
            release_url = $releaseLookup.release_url
        }
    }
    else {
        $null
    }
}

if ($AsJson) {
    $result | ConvertTo-Json -Depth 8
    exit 0
}

Write-Output 'PassWall2 Router Plan'
Write-Output '===================='
Write-Output ("Model: {0}" -f $(if ($result.router.model) { $result.router.model } else { '(not parsed)' }))
Write-Output ("Board: {0}" -f $(if ($result.router.board_name) { $result.router.board_name } else { '(not parsed)' }))
Write-Output ("Target: {0}" -f $(if ($result.router.target) { $result.router.target } else { '(not parsed)' }))
Write-Output ("OpenWrt: {0}" -f $(if ($result.router.openwrt_release) { $result.router.openwrt_release } else { '(not parsed)' }))
Write-Output ("Package manager: {0}" -f $result.detection.recommended_package_manager)
Write-Output ("Package format: .{0}" -f $result.detection.recommended_package_format)
Write-Output ("Package manager basis: {0}" -f $result.detection.package_manager_basis)
Write-Output ("Architecture: {0}" -f $(if ($result.detection.architecture) { $result.detection.architecture } else { '(not parsed)' }))
Write-Output ("Architecture source: {0}" -f $(if ($result.detection.architecture_source) { $result.detection.architecture_source } else { '(not parsed)' }))
Write-Output ''
Write-Output 'Recommended assets:'
Write-Output ("- App package: {0}" -f $(if ($result.recommendation.app_artifact_name) { $result.recommendation.app_artifact_name } else { '(not resolved)' }))
if ($result.recommendation.app_artifact_url) {
    Write-Output ("  {0}" -f $result.recommendation.app_artifact_url)
}
Write-Output ("- Component bundle: {0}" -f $(if ($result.recommendation.component_bundle_name) { $result.recommendation.component_bundle_name } else { '(not resolved)' }))
if ($result.recommendation.component_bundle_url) {
    Write-Output ("  {0}" -f $result.recommendation.component_bundle_url)
}
Write-Output ''
Write-Output 'Installed package versions detected:'
Write-Output ("- PassWall2: {0}" -f $(if ($result.installed.package_versions.passwall2) { $result.installed.package_versions.passwall2 } else { '(not parsed)' }))
Write-Output ("- xray: {0}" -f $(if ($result.installed.package_versions.xray) { $result.installed.package_versions.xray } else { '(not parsed)' }))
Write-Output ("- sing-box: {0}" -f $(if ($result.installed.package_versions.sing_box) { $result.installed.package_versions.sing_box } else { '(not parsed)' }))
Write-Output ("- hysteria: {0}" -f $(if ($result.installed.package_versions.hysteria) { $result.installed.package_versions.hysteria } else { '(not parsed)' }))
Write-Output ("- geoview/geodata: {0}" -f $(if ($result.installed.package_versions.geoview_or_geodata) { $result.installed.package_versions.geoview_or_geodata } else { '(not parsed)' }))
Write-Output ''
Write-Output 'Runtime binary versions detected:'
Write-Output ("- xray: {0}" -f $(if ($result.installed.runtime_versions.xray) { $result.installed.runtime_versions.xray } else { '(not parsed)' }))
Write-Output ("- sing-box: {0}" -f $(if ($result.installed.runtime_versions.sing_box) { $result.installed.runtime_versions.sing_box } else { '(not parsed)' }))
Write-Output ("- hysteria: {0}" -f $(if ($result.installed.runtime_versions.hysteria) { $result.installed.runtime_versions.hysteria } else { '(not parsed)' }))
Write-Output ("- geoview: {0}" -f $(if ($result.installed.runtime_versions.geoview) { $result.installed.runtime_versions.geoview } else { '(not parsed)' }))
Write-Output ''
Write-Output 'Safe path checklist:'
foreach ($item in $result.checklist) {
    Write-Output ("- {0}" -f $item)
}
Write-Output ''
Write-Output 'Useful commands:'
foreach ($command in $result.commands) {
    Write-Output ("- {0}" -f $command)
}

if ($result.notes.Count -gt 0) {
    Write-Output ''
    Write-Output 'Notes:'
    foreach ($note in $result.notes) {
        Write-Output ("- {0}" -f $note)
    }
}
