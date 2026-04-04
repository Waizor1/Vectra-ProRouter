[CmdletBinding()]
param(
    [ValidateSet('passwall2', 'passwall')]
    [string]$App = 'passwall2',

    [string]$Arch,

    [ValidateSet('opkg', 'apk', 'any')]
    [string]$PackageManager = 'any',

    [switch]$IncludeAllAssets,

    [switch]$AsJson
)

$ErrorActionPreference = 'Stop'

$repoMap = @{
    passwall2 = 'Openwrt-Passwall/openwrt-passwall2'
    passwall  = 'Openwrt-Passwall/openwrt-passwall'
}

$packageExtension = switch ($PackageManager) {
    'opkg' { '.ipk' }
    'apk'  { '.apk' }
    default { $null }
}

$packageSuffix = switch ($PackageManager) {
    'opkg' { 'ipk' }
    'apk'  { 'apk' }
    default { $null }
}

function Convert-Asset {
    param([Parameter(Mandatory = $true)] $Asset)

    [pscustomobject]@{
        name = $Asset.name
        size = $Asset.size
        download_url = $Asset.browser_download_url
        digest = $Asset.digest
    }
}

try {
    $repo = $repoMap[$App]
    $uri = "https://api.github.com/repos/$repo/releases/latest"
    $headers = @{ 'User-Agent' = 'Codex' }
    $release = Invoke-RestMethod -Headers $headers -Uri $uri
    $assets = @($release.assets)

    if ($packageExtension) {
        $matchingPackages = @($assets | Where-Object { $_.name -like "*$packageExtension" })
    }
    else {
        $matchingPackages = @($assets | Where-Object { $_.name -match '\.(ipk|apk)$' })
    }

    if ($Arch) {
        $matchingArchAssets = @($assets | Where-Object { $_.name -match [regex]::Escape($Arch) })
    }
    else {
        $matchingArchAssets = @()
    }

    $recommended = @()

    if ($App -eq 'passwall2') {
        if ($packageExtension) {
            $recommended += @($matchingPackages | Where-Object { $_.name -match '^luci-app-passwall2.*' } | Select-Object -First 1)
        }
        else {
            $recommended += @($matchingPackages | Where-Object { $_.name -match '^luci-app-passwall2.*' } | Select-Object -First 2)
        }

        if ($Arch -and $packageSuffix) {
            $expectedBundle = "passwall_packages_${packageSuffix}_${Arch}.zip"
            $recommended += @($assets | Where-Object { $_.name -eq $expectedBundle } | Select-Object -First 1)
        }
    }
    elseif ($App -eq 'passwall') {
        if ($packageExtension) {
            $recommended += @($matchingPackages | Select-Object -First 2)
        }
        else {
            $recommended += @($matchingPackages | Select-Object -First 4)
        }
    }

    $recommended = @($recommended | Where-Object { $_ })

    $result = [pscustomobject]@{
        app = $App
        repo = $repo
        tag = $release.tag_name
        published_at = $release.published_at
        release_url = $release.html_url
        package_manager = $PackageManager
        arch = $Arch
        recommended_assets = @($recommended | ForEach-Object { Convert-Asset $_ })
        matching_package_assets = @($matchingPackages | ForEach-Object { Convert-Asset $_ })
        matching_arch_assets = @($matchingArchAssets | ForEach-Object { Convert-Asset $_ })
        all_assets = @()
    }

    if ($IncludeAllAssets) {
        $result.all_assets = @($assets | ForEach-Object { Convert-Asset $_ })
    }

    if ($AsJson) {
        $result | ConvertTo-Json -Depth 8
        exit 0
    }

    Write-Output ("App: {0}" -f $result.app)
    Write-Output ("Repo: {0}" -f $result.repo)
    Write-Output ("Tag: {0}" -f $result.tag)
    Write-Output ("Published: {0}" -f $result.published_at)
    Write-Output ("Release: {0}" -f $result.release_url)
    if ($Arch) {
        Write-Output ("Arch: {0}" -f $Arch)
    }
    Write-Output ("Package manager: {0}" -f $PackageManager)
    Write-Output ""
    Write-Output "Recommended assets:"
    if ($result.recommended_assets.Count -eq 0) {
        Write-Output "  (none matched current filters)"
    }
    else {
        foreach ($asset in $result.recommended_assets) {
            Write-Output ("  - {0}" -f $asset.name)
            Write-Output ("    {0}" -f $asset.download_url)
        }
    }

    if ($result.matching_arch_assets.Count -gt 0) {
        Write-Output ""
        Write-Output "Matching arch assets:"
        foreach ($asset in $result.matching_arch_assets) {
            Write-Output ("  - {0}" -f $asset.name)
        }
    }
}
catch {
    Write-Error $_
    exit 1
}

