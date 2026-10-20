param(
    [string]$Addr = ":8080",
    [string]$DataDir = "./data",
    [string]$SenseNovaKey = "",
    [string]$AgnesKey = "",
    [switch]$NoBuild,
    [switch]$NoOpen
)

$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

if (-not $NoBuild) {
    Write-Host "==> Building frontend + backend (imgenweb)..."
    if (Get-Command task -ErrorAction SilentlyContinue) {
        task
    } else {
        pnpm --dir web install --frozen-lockfile
        pnpm --dir web run build
        go build -tags imgenweb -o imgen.exe ./cmd/imgen
    }
    if ($LASTEXITCODE -ne 0) {
        throw "Build failed"
    }
}

$env:IMGEN_ADDR = $Addr
$env:IMGEN_DATA = $DataDir

$port = ($Addr -split ':')[-1]
$url = "http://localhost:$port"

Write-Host "==> Starting Imgen: $Addr (data: $DataDir)"
Write-Host "    Browser: $url"

$proc = Start-Process -FilePath (Join-Path $PSScriptRoot "imgen.exe") `
    -ArgumentList @("serve", "--addr", $Addr, "--data", $DataDir) `
    -WindowStyle Hidden -PassThru

try {
    $ready = $false
    for ($i = 0; $i -lt 30; $i++) {
        Start-Sleep -Milliseconds 500
        try {
            $null = Invoke-RestMethod -Uri "$url/api/v1/health" -TimeoutSec 2
            $ready = $true
            break
        } catch {
            # 服务尚未就绪，继续等
        }
    }
    if (-not $ready) {
        throw "Service start timed out"
    }

    if ($SenseNovaKey -or $AgnesKey) {
        Write-Host "==> Configuring API keys..."
        $base = "$url/api/v1/providers"
        if ($SenseNovaKey) {
            $body = @{ apiKey = $SenseNovaKey } | ConvertTo-Json -Compress
            $null = Invoke-RestMethod -Method Patch -Uri "$base/sensenova" -ContentType "application/json" -Body $body
            Write-Host "    SenseNova key configured"
        }
        if ($AgnesKey) {
            $body = @{ apiKey = $AgnesKey } | ConvertTo-Json -Compress
            $null = Invoke-RestMethod -Method Patch -Uri "$base/agnes" -ContentType "application/json" -Body $body
            Write-Host "    Agnes key configured"
        }
    }

    if (-not $NoOpen) {
        Start-Process $url
    }

    Write-Host "==> Imgen running (PID $($proc.Id)), Ctrl+C to stop"
    Wait-Process -Id $proc.Id
} finally {
    if (-not $proc.HasExited) {
        Stop-Process -Id $proc.Id -Force
    }
}
