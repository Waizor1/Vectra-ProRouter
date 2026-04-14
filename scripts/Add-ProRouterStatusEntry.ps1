param(
    [Parameter(Mandatory = $true)]
    [string]$Summary,
    [string[]]$Modules = @(),
    [string[]]$NextSteps = @(),
    [string[]]$Decisions = @(),
    [string]$VaultRoot = '',
    [datetime]$Now = (Get-Date)
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

if ([string]::IsNullOrWhiteSpace($VaultRoot)) {
    $scriptRoot = if ($PSScriptRoot) { $PSScriptRoot } else { Split-Path -Parent $PSCommandPath }
    $VaultRoot = Join-Path (Split-Path -Parent $scriptRoot) 'ProRouter'
}

function Format-ModuleReference {
    param(
        [string]$Value,
        [string]$ResolvedVaultRoot
    )

    if ([string]::IsNullOrWhiteSpace($Value)) {
        return $null
    }

    if ($Value -match '^\[\[.+\]\]$') {
        return $Value
    }

    $candidatePath = Join-Path $ResolvedVaultRoot ("02 Modules\{0}.md" -f $Value)
    if (Test-Path -LiteralPath $candidatePath) {
        return "[[02 Modules/$Value]]"
    }

    return ('`{0}`' -f $Value)
}

$resolvedVaultRoot = if (Test-Path -LiteralPath $VaultRoot) {
    (Resolve-Path -LiteralPath $VaultRoot).Path
} else {
    New-Item -ItemType Directory -Path $VaultRoot -Force | Out-Null
    (Resolve-Path -LiteralPath $VaultRoot).Path
}

$dailyDir = Join-Path $resolvedVaultRoot '04 Sessions\Daily'
if (-not (Test-Path -LiteralPath $dailyDir)) {
    New-Item -ItemType Directory -Path $dailyDir -Force | Out-Null
}

$dateLabel = $Now.ToString('yyyy-MM-dd')
$timeLabel = $Now.ToString('HH:mm')
$dailyPath = Join-Path $dailyDir ("{0}.md" -f $dateLabel)

if (-not (Test-Path -LiteralPath $dailyPath)) {
    $initialContent = @(
        '---',
        'type: session',
        "date: $dateLabel",
        'tags:',
        '  - session',
        '---',
        '',
        "# Session $dateLabel",
        '',
        '## Summary',
        '',
        '-',
        '',
        '## Completion Updates',
        ''
    )
    Set-Content -LiteralPath $dailyPath -Value $initialContent -Encoding UTF8
}
else {
    $existingContent = Get-Content -LiteralPath $dailyPath -Raw
    if ($existingContent -notmatch '(?m)^## Completion Updates\s*$') {
        Add-Content -LiteralPath $dailyPath -Value @('', '## Completion Updates', '') -Encoding UTF8
    }
}

$moduleLinks = @(
    $Modules |
        ForEach-Object { Format-ModuleReference -Value $_ -ResolvedVaultRoot $resolvedVaultRoot } |
        Where-Object { $_ }
)

$entry = New-Object System.Collections.Generic.List[string]
$entry.Add('')
$entry.Add("### $timeLabel")
$entry.Add('')
$entry.Add("- Summary: $Summary")

if ($moduleLinks.Count -gt 0) {
    $entry.Add("- Modules: $($moduleLinks -join ', ')")
}

if ($Decisions.Count -gt 0) {
    $entry.Add("- Decisions: $($Decisions -join '; ')")
}

if ($NextSteps.Count -gt 0) {
    $entry.Add("- Next: $($NextSteps -join '; ')")
}

Add-Content -LiteralPath $dailyPath -Value $entry -Encoding UTF8
Write-Output "Updated $dailyPath"
