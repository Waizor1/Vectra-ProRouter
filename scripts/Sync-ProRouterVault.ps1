param(
    [string]$VaultRoot = (Join-Path (Split-Path -Parent $PSScriptRoot) 'ProRouter'),
    [int]$Depth = 3
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$repoRoot = Split-Path -Parent $PSScriptRoot
$resolvedVaultRoot = if (Test-Path -LiteralPath $VaultRoot) {
    (Resolve-Path -LiteralPath $VaultRoot).Path
} else {
    New-Item -ItemType Directory -Path $VaultRoot -Force | Out-Null
    (Resolve-Path -LiteralPath $VaultRoot).Path
}

$dashboardDir = Join-Path $resolvedVaultRoot '00 Dashboard'
if (-not (Test-Path -LiteralPath $dashboardDir)) {
    New-Item -ItemType Directory -Path $dashboardDir -Force | Out-Null
}

$repoMapPath = Join-Path $dashboardDir 'Repo Map.md'
$generatedAt = Get-Date -Format 'yyyy-MM-dd HH:mm:ss zzz'
$repoName = Split-Path -Leaf $repoRoot

$skipNames = @(
    '.git',
    '.obsidian',
    '.next',
    'coverage',
    'dist',
    'node_modules'
    '98 Local'
)

$skipPrefixes = @(
    (Join-Path $repoRoot 'deploy\runtime'),
    (Join-Path $repoRoot 'passwall2\.git'),
    (Join-Path $repoRoot 'openwrt-24.10-src\.git'),
    (Join-Path $repoRoot 'procd-src\.git')
) | ForEach-Object {
    [System.IO.Path]::GetFullPath($_)
}

function Test-SkippedPath {
    param(
        [System.IO.FileSystemInfo]$Item
    )

    if ($skipNames -contains $Item.Name) {
        return $true
    }

    $fullPath = [System.IO.Path]::GetFullPath($Item.FullName)
    foreach ($prefix in $skipPrefixes) {
        if ($fullPath.StartsWith($prefix, [System.StringComparison]::OrdinalIgnoreCase)) {
            return $true
        }
    }

    return $false
}

function Get-VisibleChildren {
    param(
        [string]$Path
    )

    Get-ChildItem -LiteralPath $Path -Force |
        Where-Object { -not (Test-SkippedPath -Item $_) } |
        Sort-Object @{ Expression = { -not $_.PSIsContainer } }, Name
}

function Get-TreeLines {
    param(
        [string]$Path,
        [string]$Prefix = '',
        [int]$CurrentDepth = 0,
        [int]$MaxDepth = 3
    )

    if ($CurrentDepth -ge $MaxDepth) {
        return @()
    }

    $children = @(Get-VisibleChildren -Path $Path)
    $lines = New-Object System.Collections.Generic.List[string]

    for ($index = 0; $index -lt $children.Count; $index++) {
        $child = $children[$index]
        $isLast = $index -eq ($children.Count - 1)
        $branch = if ($isLast) { '\- ' } else { '|- ' }
        $name = if ($child.PSIsContainer) { "$($child.Name)/" } else { $child.Name }
        $lines.Add("$Prefix$branch$name")

        if ($child.PSIsContainer) {
            $nextPrefix = if ($isLast) { "$Prefix   " } else { "$Prefix|  " }
            foreach ($line in Get-TreeLines -Path $child.FullName -Prefix $nextPrefix -CurrentDepth ($CurrentDepth + 1) -MaxDepth $MaxDepth) {
                $lines.Add($line)
            }
        }
    }

    return $lines.ToArray()
}

$moduleTable = @(
    @{ Name = 'Knowledge base and runbooks'; Path = 'ai_docs/, scripts/, RTK.md'; Note = '[[02 Modules/Knowledge Base and Runbooks]]' },
    @{ Name = 'Web control plane'; Path = 'apps/web'; Note = '[[02 Modules/Web Control Plane]]' },
    @{ Name = 'Shared contracts'; Path = 'packages/contracts'; Note = '[[02 Modules/Shared Contracts]]' },
    @{ Name = 'Shared database'; Path = 'packages/db'; Note = '[[02 Modules/Shared Database]]' },
    @{ Name = 'Router agent'; Path = 'router/vectra-controller-agent'; Note = '[[02 Modules/Router Agent]]' },
    @{ Name = 'LuCI controller package'; Path = 'router/luci-app-vectra-controller'; Note = '[[02 Modules/LuCI Controller Package]]' },
    @{ Name = 'Deployment stack'; Path = 'deploy/'; Note = '[[02 Modules/Deployment Stack]]' },
    @{ Name = 'Source mirrors'; Path = 'passwall2/, openwrt-24.10-src/, procd-src/'; Note = '[[02 Modules/Source Mirrors]]' }
)

$topLevel = @(Get-VisibleChildren -Path $repoRoot)
$topLevelDirectories = @($topLevel | Where-Object { $_.PSIsContainer }).Count
$topLevelFiles = @($topLevel | Where-Object { -not $_.PSIsContainer }).Count
$treeLines = @("./") + @(Get-TreeLines -Path $repoRoot -CurrentDepth 0 -MaxDepth $Depth)
$moduleLines = $moduleTable | ForEach-Object { "| $($_.Name) | ``$($_.Path)`` | $($_.Note) |" }

$content = @(
    '---',
    'type: generated',
    "updated: '$generatedAt'",
    'generated-by: scripts/Sync-ProRouterVault.ps1',
    'tags:',
    '  - generated',
    '  - structure',
    '---',
    '',
    '# Repo Map',
    '',
    "Generated from the current workspace root ``$repoName``.",
    '',
    '## Snapshot',
    '',
    "- Generated at: ``$generatedAt``",
    "- Top-level directories: ``$topLevelDirectories``",
    "- Top-level files: ``$topLevelFiles``",
    "- Tree depth: ``$Depth``",
    '',
    '## Module Notes',
    '',
    '| Area | Path | Note |',
    '|---|---|---|'
) + $moduleLines + @(
    '',
    '## Structure',
    '',
    '```text'
) + $treeLines + @(
    '```'
)

Set-Content -LiteralPath $repoMapPath -Value $content -Encoding UTF8
Write-Output "Updated $repoMapPath"
