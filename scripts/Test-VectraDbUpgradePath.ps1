param(
    [string]$Image = 'public.ecr.aws/docker/library/postgres:16-bookworm'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$containerName = 'vectra-db-upgrade-smoke-{0}' -f ([Guid]::NewGuid().ToString('N').Substring(0, 12))
$databaseName = 'vectra'
$databaseUser = 'vectra'
$databasePassword = 'vectra-test-password'
$started = $false

function Invoke-Docker {
    param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments)

    & docker @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "docker $($Arguments -join ' ') failed with exit code $LASTEXITCODE"
    }
}

try {
    $runOutput = & docker run -d --rm `
        --name $containerName `
        -e "POSTGRES_DB=$databaseName" `
        -e "POSTGRES_USER=$databaseUser" `
        -e "POSTGRES_PASSWORD=$databasePassword" `
        -p '127.0.0.1::5432' `
        $Image 2>&1
    $containerId = ($runOutput | Select-Object -First 1).ToString().Trim()
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($containerId)) {
        throw "Failed to start disposable PostgreSQL container. Ensure Docker Desktop is running. Docker output: $runOutput"
    }
    $started = $true

    $deadline = (Get-Date).AddSeconds(90)
    do {
        Start-Sleep -Seconds 2
        & docker exec $containerName pg_isready -U $databaseUser -d $databaseName *> $null
        if ($LASTEXITCODE -eq 0) {
            break
        }
    } while ((Get-Date) -lt $deadline)

    if ($LASTEXITCODE -ne 0) {
        throw 'Disposable PostgreSQL did not become ready before timeout.'
    }

    $portLine = (& docker port $containerName '5432/tcp').Trim()
    if ($LASTEXITCODE -ne 0 -or $portLine -notmatch ':(\d+)$') {
        throw 'Failed to resolve disposable PostgreSQL host port.'
    }

    $hostPort = $Matches[1]
    $env:DATABASE_URL = "postgresql://${databaseUser}:${databasePassword}@127.0.0.1:${hostPort}/${databaseName}"
    $env:VECTRA_DB_UPGRADE_TEST_ALLOW_RESET = '1'

    node .\apps\web\scripts\verify-db-upgrade-path.mjs --reset-schema
    if ($LASTEXITCODE -ne 0) {
        throw "verify-db-upgrade-path.mjs failed with exit code $LASTEXITCODE"
    }
} finally {
    Remove-Item Env:\DATABASE_URL -ErrorAction SilentlyContinue
    Remove-Item Env:\VECTRA_DB_UPGRADE_TEST_ALLOW_RESET -ErrorAction SilentlyContinue

    if ($started) {
        & docker rm -f $containerName *> $null
    }
}
