[CmdletBinding()]
param(
    [string]$Tag = '26.4.10-1',

    [string]$Arch = 'aarch64_cortex-a53',

    [Parameter(Mandatory = $true)]
    [string]$OutputDir,

    [switch]$IncludeOptional
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

Add-Type -AssemblyName System.IO.Compression.FileSystem

$requiredMirroredPackages = @(
    'tcping',
    'xray-core',
    'geoview',
    'v2ray-geoip',
    'v2ray-geosite',
    'chinadns-ng',
    'luci-app-passwall2'
)

$optionalMirroredPackages = @(
    'sing-box',
    'hysteria'
)

$openWrtFeedProvidedDependencies = @(
    'libc',
    'coreutils',
    'coreutils-base64',
    'coreutils-nohup',
    'curl',
    'ip-full',
    'libuci-lua',
    'lua',
    'luci-compat',
    'luci-lib-jsonc',
    'resolveip',
    'unzip',
    'luci-lua-runtime'
)

$bundlePackagePatterns = [ordered]@{
    'tcping'      = '^tcping_.*\.ipk$'
    'xray-core'   = '^xray-core_.*\.ipk$'
    'geoview'     = '^geoview_.*\.ipk$'
    'v2ray-geoip' = '^v2ray-geoip_.*\.ipk$'
    'v2ray-geosite' = '^v2ray-geosite_.*\.ipk$'
    'chinadns-ng' = '^chinadns-ng_.*\.ipk$'
    'sing-box'    = '^sing-box_.*\.ipk$'
    'hysteria'    = '^hysteria_.*\.ipk$'
}

function Write-Info {
    param([string]$Message)

    Write-Output $Message
}

function Get-TempDirectory {
    $path = Join-Path ([System.IO.Path]::GetTempPath()) ("pw-bootstrap-" + [guid]::NewGuid())
    New-Item -ItemType Directory -Path $path | Out-Null
    return $path
}

function Download-File {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Url,

        [Parameter(Mandatory = $true)]
        [string]$Destination
    )

    Invoke-WebRequest -Uri $Url -OutFile $Destination
}

function Expand-IpkControl {
    param(
        [Parameter(Mandatory = $true)]
        [string]$IpkPath,

        [Parameter(Mandatory = $true)]
        [string]$DestinationRoot
    )

    $ipkExtractDir = Join-Path $DestinationRoot 'ipk'
    $controlExtractDir = Join-Path $DestinationRoot 'control'
    New-Item -ItemType Directory -Path $ipkExtractDir | Out-Null
    New-Item -ItemType Directory -Path $controlExtractDir | Out-Null

    & tar -xf $IpkPath -C $ipkExtractDir | Out-Null
    $controlTar = Get-ChildItem -Path $ipkExtractDir | Where-Object { $_.Name -like 'control.tar*' } | Select-Object -First 1
    if (-not $controlTar) {
        throw "control.tar archive was not found in $IpkPath"
    }

    if ($controlTar.Name -like '*.gz') {
        & tar -xzf $controlTar.FullName -C $controlExtractDir | Out-Null
    }
    elseif ($controlTar.Name -like '*.zst') {
        & tar --zstd -xf $controlTar.FullName -C $controlExtractDir | Out-Null
    }
    else {
        & tar -xf $controlTar.FullName -C $controlExtractDir | Out-Null
    }

    $controlFile = Join-Path $controlExtractDir 'control'
    if (-not (Test-Path -LiteralPath $controlFile)) {
        throw "control file was not found after extracting $IpkPath"
    }

    return Get-Content -LiteralPath $controlFile -Raw
}

function Parse-ControlDependencies {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ControlText
    )

    $dependsLine = ($ControlText -split "`r?`n" | Where-Object { $_ -like 'Depends: *' } | Select-Object -First 1)
    if (-not $dependsLine) {
        throw 'Depends line was not found in luci-app-passwall2 control metadata'
    }

    return @(
        $dependsLine.Replace('Depends: ', '').Split(',') |
            ForEach-Object { $_.Trim() } |
            Where-Object { $_ }
    )
}

function Parse-ControlField {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ControlText,

        [Parameter(Mandatory = $true)]
        [string]$FieldName
    )

    $line = ($ControlText -split "`r?`n" | Where-Object { $_ -like "$FieldName: *" } | Select-Object -First 1)
    if (-not $line) {
        throw "$FieldName line was not found in control metadata"
    }

    return $line.Replace("$FieldName: ", '').Trim()
}

function Resolve-IpkMetadata {
    param(
        [Parameter(Mandatory = $true)]
        [string]$IpkPath,

        [Parameter(Mandatory = $true)]
        [string]$TempRoot
    )

    $controlText = Expand-IpkControl -IpkPath $IpkPath -DestinationRoot $TempRoot
    [pscustomobject]@{
        version = Parse-ControlField -ControlText $controlText -FieldName 'Version'
        installedSizeBytes = [int64](Parse-ControlField -ControlText $controlText -FieldName 'Installed-Size')
        downloadSizeBytes = [int64](Get-Item -LiteralPath $IpkPath).Length
    }
}

function Resolve-Release {
    param(
        [Parameter(Mandatory = $true)]
        [string]$TagName
    )

    $headers = @{ 'User-Agent' = 'Codex' }
    return Invoke-RestMethod -Headers $headers -Uri "https://api.github.com/repos/Openwrt-Passwall/openwrt-passwall2/releases/tags/$TagName"
}

function Resolve-Asset {
    param(
        [Parameter(Mandatory = $true)]
        $Release,

        [Parameter(Mandatory = $true)]
        [string]$AssetName
    )

    $asset = $Release.assets | Where-Object { $_.name -eq $AssetName } | Select-Object -First 1
    if (-not $asset) {
        throw "Asset $AssetName was not found in release $($Release.tag_name)"
    }

    return $asset
}

function Resolve-LuciAsset {
    param(
        [Parameter(Mandatory = $true)]
        $Release
    )

    $asset = $Release.assets |
        Where-Object { $_.name -match '^luci-app-passwall2_.*_all\.ipk$' } |
        Select-Object -First 1
    if (-not $asset) {
        throw "luci-app-passwall2 .ipk asset was not found in release $($Release.tag_name)"
    }

    return $asset
}

function Resolve-ZipPackageName {
    param(
        [Parameter(Mandatory = $true)]
        [System.IO.Compression.ZipArchive]$Archive,

        [Parameter(Mandatory = $true)]
        [string]$PackageName
    )

    $pattern = $bundlePackagePatterns[$PackageName]
    if (-not $pattern) {
        throw "No bundle filename pattern is defined for package $PackageName"
    }

    $entry = $Archive.Entries |
        Where-Object { $_.FullName -match $pattern } |
        Select-Object -First 1
    if (-not $entry) {
        throw "Package $PackageName was not found in the upstream PassWall bundle"
    }

    return $entry.FullName
}

function Extract-ZipEntry {
    param(
        [Parameter(Mandatory = $true)]
        [System.IO.Compression.ZipArchive]$Archive,

        [Parameter(Mandatory = $true)]
        [string]$EntryName,

        [Parameter(Mandatory = $true)]
        [string]$DestinationPath
    )

    $entry = $Archive.Entries | Where-Object { $_.FullName -eq $EntryName } | Select-Object -First 1
    if (-not $entry) {
        throw "Entry $EntryName was not found in the upstream PassWall bundle"
    }

    $entryStream = $entry.Open()
    try {
        $destinationStream = [System.IO.File]::Create($DestinationPath)
        try {
            $entryStream.CopyTo($destinationStream)
        }
        finally {
            $destinationStream.Dispose()
        }
    }
    finally {
        $entryStream.Dispose()
    }
}

function New-ManifestEntry {
    param(
        [Parameter(Mandatory = $true)]
        [string]$PackageName,

        [Parameter(Mandatory = $true)]
        [System.Collections.IDictionary]$ResolvedPackageFiles,

        [Parameter(Mandatory = $true)]
        [System.Collections.IDictionary]$ResolvedPackageMetadata
    )

    $metadata = $ResolvedPackageMetadata[$PackageName]
    [pscustomobject]@{
        name = $PackageName
        filename = $ResolvedPackageFiles[$PackageName]
        version = $metadata.version
        downloadSizeBytes = [int64]$metadata.downloadSizeBytes
        installedSizeBytes = [int64]$metadata.installedSizeBytes
    }
}

$targetOutputDir = [System.IO.Path]::GetFullPath($OutputDir)
$tempDir = Get-TempDirectory

try {
    Write-Info "Fetching upstream release metadata for tag $Tag"
    $release = Resolve-Release -TagName $Tag
    $luciAsset = Resolve-LuciAsset -Release $release
    $bundleAssetName = "passwall_packages_ipk_${Arch}.zip"
    $bundleAsset = Resolve-Asset -Release $release -AssetName $bundleAssetName

    $resolvedPackageFiles = [ordered]@{}
    $resolvedPackageMetadata = [ordered]@{}
    $resolvedPackageFiles['luci-app-passwall2'] = $luciAsset.name

    Write-Info "Downloading upstream luci-app-passwall2: $($luciAsset.browser_download_url)"
    $luciPackagePath = Join-Path $tempDir $luciAsset.name
    Download-File -Url $luciAsset.browser_download_url -Destination $luciPackagePath

    Write-Info "Downloading upstream bundle: $($bundleAsset.browser_download_url)"
    $bundlePath = Join-Path $tempDir $bundleAsset.name
    Download-File -Url $bundleAsset.browser_download_url -Destination $bundlePath

    Write-Info 'Reading luci-app-passwall2 control metadata and validating dependency coverage...'
    $controlText = Expand-IpkControl -IpkPath $luciPackagePath -DestinationRoot (Join-Path $tempDir 'luci-control')
    $resolvedPackageMetadata['luci-app-passwall2'] = [pscustomobject]@{
        version = Parse-ControlField -ControlText $controlText -FieldName 'Version'
        installedSizeBytes = [int64](Parse-ControlField -ControlText $controlText -FieldName 'Installed-Size')
        downloadSizeBytes = [int64](Get-Item -LiteralPath $luciPackagePath).Length
    }
    $mirroredDependencyCoverage = [System.Collections.Generic.HashSet[string]]::new([string[]]$requiredMirroredPackages)
    $openWrtDependencyCoverage = [System.Collections.Generic.HashSet[string]]::new([string[]]$openWrtFeedProvidedDependencies)
    $missingDependencies = @()
    foreach ($dependency in (Parse-ControlDependencies -ControlText $controlText)) {
        if (-not $mirroredDependencyCoverage.Contains($dependency) -and -not $openWrtDependencyCoverage.Contains($dependency)) {
            $missingDependencies += $dependency
        }
    }

    if ($missingDependencies.Count -gt 0) {
        throw "Uncovered luci-app-passwall2 dependencies: $($missingDependencies -join ', ')"
    }

    if (-not (Test-Path -LiteralPath $targetOutputDir)) {
        New-Item -ItemType Directory -Path $targetOutputDir -Force | Out-Null
    }

    Copy-Item -LiteralPath $luciPackagePath -Destination (Join-Path $targetOutputDir $luciAsset.name) -Force

    $bundleArchive = [System.IO.Compression.ZipFile]::OpenRead($bundlePath)
    try {
        $requiredFromBundle = $requiredMirroredPackages | Where-Object { $_ -ne 'luci-app-passwall2' }
        foreach ($packageName in $requiredFromBundle) {
            $entryName = Resolve-ZipPackageName -Archive $bundleArchive -PackageName $packageName
            $resolvedPackageFiles[$packageName] = $entryName
            Write-Info "Publishing required mirrored package: $entryName"
            $publishedPath = Join-Path $targetOutputDir $entryName
            Extract-ZipEntry -Archive $bundleArchive -EntryName $entryName -DestinationPath $publishedPath
            $resolvedPackageMetadata[$packageName] = Resolve-IpkMetadata -IpkPath $publishedPath -TempRoot (Join-Path $tempDir ("meta-" + $packageName))
        }

        $publishedOptionalPackages = @()
        if ($IncludeOptional) {
            foreach ($packageName in $optionalMirroredPackages) {
                $entryName = Resolve-ZipPackageName -Archive $bundleArchive -PackageName $packageName
                $resolvedPackageFiles[$packageName] = $entryName
                Write-Info "Publishing optional mirrored package: $entryName"
                $publishedPath = Join-Path $targetOutputDir $entryName
                Extract-ZipEntry -Archive $bundleArchive -EntryName $entryName -DestinationPath $publishedPath
                $resolvedPackageMetadata[$packageName] = Resolve-IpkMetadata -IpkPath $publishedPath -TempRoot (Join-Path $tempDir ("meta-" + $packageName))
                $publishedOptionalPackages += $packageName
            }
        }
    }
    finally {
        $bundleArchive.Dispose()
    }

    $manifest = [pscustomobject]@{
        tag = $Tag
        arch = $Arch
        requiredPackages = @($requiredMirroredPackages | ForEach-Object { New-ManifestEntry -PackageName $_ -ResolvedPackageFiles $resolvedPackageFiles -ResolvedPackageMetadata $resolvedPackageMetadata })
        optionalPackages = @($publishedOptionalPackages | ForEach-Object { New-ManifestEntry -PackageName $_ -ResolvedPackageFiles $resolvedPackageFiles -ResolvedPackageMetadata $resolvedPackageMetadata })
        sourceUrls = [pscustomobject]@{
            release = $release.html_url
            luciAppPackage = $luciAsset.browser_download_url
            packageBundle = $bundleAsset.browser_download_url
        }
    }

    $manifestPath = Join-Path $targetOutputDir 'manifest.json'
    $manifest | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $manifestPath -Encoding UTF8

    $expectedFiles = @(
        $requiredMirroredPackages | ForEach-Object { $resolvedPackageFiles[$_] }
    )
    if ($IncludeOptional) {
        $expectedFiles += @(
            $optionalMirroredPackages | ForEach-Object { $resolvedPackageFiles[$_] }
        )
    }

    $missingFiles = @(
        $expectedFiles | Where-Object { -not (Test-Path -LiteralPath (Join-Path $targetOutputDir $_)) }
    )
    if ($missingFiles.Count -gt 0) {
        throw "Expected files are missing after publish: $($missingFiles -join ', ')"
    }

    Write-Info "Done. Bootstrap mirror published to $targetOutputDir"
    Write-Info "Manifest: $manifestPath"
}
finally {
    Remove-Item -LiteralPath $tempDir -Recurse -Force -ErrorAction SilentlyContinue
}
