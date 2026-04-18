Set-StrictMode -Version Latest

function Get-OptionalCommandPath {
    param(
        [Parameter(Mandatory = $true)]
        [string[]]$Candidates
    )

    foreach ($candidate in $Candidates) {
        $command = Get-Command $candidate -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($command) {
            return $command.Source
        }
    }

    return $null
}

function Get-RequiredCommandPath {
    param(
        [Parameter(Mandatory = $true)]
        [string[]]$Candidates,
        [Parameter(Mandatory = $true)]
        [string]$ErrorMessage
    )

    $path = Get-OptionalCommandPath -Candidates $Candidates
    if ($path) {
        return $path
    }

    throw $ErrorMessage
}

function Resolve-OpenSshKnownHostsFile {
    param([string]$Path)

    if ([string]::IsNullOrWhiteSpace($Path)) {
        throw 'OpenSSH transport requires -OpenSshKnownHostsFile or OPENWRT_ROUTER_KNOWN_HOSTS_FILE.'
    }

    if (-not (Test-Path -LiteralPath $Path)) {
        throw "OpenSSH known_hosts file was not found: $Path"
    }

    return (Resolve-Path -LiteralPath $Path).Path
}

function Resolve-OpenWrtTransportSpec {
    param(
        [ValidateSet('Auto', 'PuTTY', 'OpenSSH')]
        [string]$Transport = 'Auto',
        [string]$RouterPassword,
        [string]$RouterHostKey,
        [string]$OpenSshKnownHostsFile,
        [string]$OpenSshIdentityFile,
        [switch]$NeedsUpload
    )

    $puttyPlink = Get-OptionalCommandPath -Candidates @('plink', 'plink.exe')
    $puttyPscp = if ($NeedsUpload) { Get-OptionalCommandPath -Candidates @('pscp', 'pscp.exe') } else { $null }
    $sshPath = Get-OptionalCommandPath -Candidates @('ssh')
    $scpPath = if ($NeedsUpload) { Get-OptionalCommandPath -Candidates @('scp') } else { $null }

    $resolvedIdentityFile = $null
    if (-not [string]::IsNullOrWhiteSpace($OpenSshIdentityFile)) {
        if (-not (Test-Path -LiteralPath $OpenSshIdentityFile)) {
            throw "OpenSSH identity file was not found: $OpenSshIdentityFile"
        }
        $resolvedIdentityFile = (Resolve-Path -LiteralPath $OpenSshIdentityFile).Path
    }

    $selectedTransport = switch ($Transport) {
        'PuTTY' { 'PuTTY' }
        'OpenSSH' { 'OpenSSH' }
        default {
            if (-not [string]::IsNullOrWhiteSpace($RouterPassword)) {
                'PuTTY'
            }
            elseif (-not [string]::IsNullOrWhiteSpace($OpenSshKnownHostsFile)) {
                'OpenSSH'
            }
            elseif (-not [string]::IsNullOrWhiteSpace($RouterHostKey)) {
                'PuTTY'
            }
            else {
                throw 'Auto transport could not choose a safe path. Provide RouterPassword or RouterHostKey for PuTTY, or OpenSshKnownHostsFile for OpenSSH.'
            }
        }
    }

    if ($selectedTransport -eq 'PuTTY') {
        if ([string]::IsNullOrWhiteSpace($RouterHostKey)) {
            throw 'PuTTY transport requires RouterHostKey / OPENWRT_ROUTER_HOSTKEY.'
        }

        $plinkPath = if ($puttyPlink) { $puttyPlink } else {
            throw 'plink was not found. Install PuTTY (for example via Homebrew on macOS) or add plink/plink.exe to PATH.'
        }

        $pscpPath = $null
        if ($NeedsUpload) {
            if ($puttyPscp) {
                $pscpPath = $puttyPscp
            }
            else {
                throw 'pscp was not found. Install PuTTY (for example via Homebrew on macOS) or add pscp/pscp.exe to PATH.'
            }
        }

        return [pscustomobject]@{
            mode = 'PuTTY'
            plinkPath = $plinkPath
            pscpPath = $pscpPath
            sshPath = $null
            scpPath = $null
            knownHostsFile = $null
            identityFile = $null
        }
    }

    if (-not [string]::IsNullOrWhiteSpace($RouterPassword)) {
        throw 'OpenSSH transport in this workspace is key-based only. Omit RouterPassword and use ssh-agent, ~/.ssh/config, or OpenSshIdentityFile.'
    }

    $resolvedKnownHostsFile = Resolve-OpenSshKnownHostsFile -Path $OpenSshKnownHostsFile
    $resolvedSshPath = if ($sshPath) { $sshPath } else {
        throw 'ssh was not found in PATH.'
    }
    $resolvedScpPath = $null
    if ($NeedsUpload) {
        if ($scpPath) {
            $resolvedScpPath = $scpPath
        }
        else {
            throw 'scp was not found in PATH.'
        }
    }

    return [pscustomobject]@{
        mode = 'OpenSSH'
        plinkPath = $null
        pscpPath = $null
        sshPath = $resolvedSshPath
        scpPath = $resolvedScpPath
        knownHostsFile = $resolvedKnownHostsFile
        identityFile = $resolvedIdentityFile
    }
}

function New-OpenSshArgs {
    param(
        [Parameter(Mandatory = $true)]
        [psobject]$TransportSpec,
        [Parameter(Mandatory = $true)]
        [string]$RouterUser
    )

    $args = @(
        '-o', 'BatchMode=yes',
        '-o', 'StrictHostKeyChecking=yes',
        '-o', ('UserKnownHostsFile={0}' -f $TransportSpec.knownHostsFile),
        '-o', ('User={0}' -f $RouterUser)
    )

    if (-not [string]::IsNullOrWhiteSpace($TransportSpec.identityFile)) {
        $args += @('-o', 'IdentitiesOnly=yes', '-i', $TransportSpec.identityFile)
    }

    return $args
}

function New-OpenScpArgs {
    param(
        [Parameter(Mandatory = $true)]
        [psobject]$TransportSpec,
        [Parameter(Mandatory = $true)]
        [string]$RouterUser
    )

    $args = @(
        '-B',
        '-o', 'BatchMode=yes',
        '-o', 'StrictHostKeyChecking=yes',
        '-o', ('UserKnownHostsFile={0}' -f $TransportSpec.knownHostsFile),
        '-o', ('User={0}' -f $RouterUser)
    )

    if (-not [string]::IsNullOrWhiteSpace($TransportSpec.identityFile)) {
        $args += @('-o', 'IdentitiesOnly=yes', '-i', $TransportSpec.identityFile)
    }

    return $args
}

function Invoke-OpenWrtRemoteCommand {
    param(
        [Parameter(Mandatory = $true)]
        [psobject]$TransportSpec,
        [Parameter(Mandatory = $true)]
        [string]$RouterHost,
        [Parameter(Mandatory = $true)]
        [string]$RouterUser,
        [string]$RouterPassword,
        [string]$RouterHostKey,
        [Parameter(Mandatory = $true)]
        [string]$CommandText,
        [switch]$ViaStdinSh
    )

    if ($TransportSpec.mode -eq 'PuTTY') {
        $args = @('-ssh', '-batch', '-no-antispoof', '-hostkey', $RouterHostKey, '-l', $RouterUser)
        if (-not [string]::IsNullOrWhiteSpace($RouterPassword)) {
            $args += @('-pw', $RouterPassword)
        }
        $args += $RouterHost
        if ($ViaStdinSh) {
            $args += 'sh -s'
            $output = $CommandText | & $TransportSpec.plinkPath @args 2>&1
        }
        else {
            $args += $CommandText
            $output = & $TransportSpec.plinkPath @args 2>&1
        }

        return [pscustomobject]@{
            exitCode = $LASTEXITCODE
            output = @($output)
            text = (@($output) -join "`n").Trim()
        }
    }

    $sshArgs = @(New-OpenSshArgs -TransportSpec $TransportSpec -RouterUser $RouterUser)
    $sshArgs += $RouterHost
    if ($ViaStdinSh) {
        $sshArgs += 'sh -s'
        $output = $CommandText | & $TransportSpec.sshPath @sshArgs 2>&1
    }
    else {
        $sshArgs += $CommandText
        $output = & $TransportSpec.sshPath @sshArgs 2>&1
    }

    return [pscustomobject]@{
        exitCode = $LASTEXITCODE
        output = @($output)
        text = (@($output) -join "`n").Trim()
    }
}

function Copy-OpenWrtUpload {
    param(
        [Parameter(Mandatory = $true)]
        [psobject]$TransportSpec,
        [Parameter(Mandatory = $true)]
        [string]$RouterHost,
        [Parameter(Mandatory = $true)]
        [string]$RouterUser,
        [string]$RouterPassword,
        [string]$RouterHostKey,
        [Parameter(Mandatory = $true)]
        [string]$SourcePath,
        [Parameter(Mandatory = $true)]
        [string]$TargetPath
    )

    if ($TransportSpec.mode -eq 'PuTTY') {
        $tempRoot = [System.IO.Path]::GetTempPath()
        $passwordFile = $null
        if (-not [string]::IsNullOrWhiteSpace($RouterPassword)) {
            $passwordFile = Join-Path $tempRoot ("putty-password-{0}.txt" -f ([guid]::NewGuid().ToString('N')))
            Set-Content -LiteralPath $passwordFile -Value $RouterPassword -NoNewline
        }

        try {
            $args = @('-batch', '-scp', '-q', '-hostkey', $RouterHostKey, '-l', $RouterUser)
            if ($passwordFile) {
                $args += @('-pwfile', $passwordFile)
            }

            if ((Get-Item -LiteralPath $SourcePath).PSIsContainer) {
                $args += '-r'
            }

            $args += @($SourcePath, ("{0}:{1}" -f $RouterHost, $TargetPath))
            & $TransportSpec.pscpPath @args
            return
        }
        finally {
            if ($passwordFile -and (Test-Path -LiteralPath $passwordFile)) {
                Remove-Item -LiteralPath $passwordFile -Force
            }
        }
    }

    $scpArgs = @(New-OpenScpArgs -TransportSpec $TransportSpec -RouterUser $RouterUser)
    if ((Get-Item -LiteralPath $SourcePath).PSIsContainer) {
        $scpArgs += '-r'
    }
    $scpArgs += @($SourcePath, ("{0}@{1}:{2}" -f $RouterUser, $RouterHost, $TargetPath))
    & $TransportSpec.scpPath @scpArgs
}
