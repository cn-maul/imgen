#Requires -Version 7.0
<#
.SYNOPSIS
    Imgen 冒烟测试：起服务 → 校验各接口形状 → 跑前端契约校验。

.DESCRIPTION
    纯 PowerShell 实现，除 go / node / pnpm（已在 PATH）外无外部依赖。

    安全约定（很重要）：
    本机仓库下的 ./data 是**用户真实数据**，里面有 Provider API Key、资产与历史。
    本脚本永远使用独立的临时数据目录，且启动前会做守卫：拒绝把 ./data、
    ./data 的子目录、或已含 imgen.db / master.key 的目录当作测试数据目录。

    临时目录默认建在仓库内的 .smoke-tmp\ 下，而不是系统 %TEMP%：
    部分受限环境（IDE / 沙箱代理）只允许子进程写工作区，写 %TEMP% 会
    `mkdir ...: Access is denied`，服务根本起不来。想要用系统临时目录可显式传 -DataDir。

    流程：
      ①（除非 -SkipBuild）go build -tags imgenweb -o <临时目录>\imgen.exe ./cmd/imgen
      ② IMGEN_ADDR=127.0.0.1:<Port> + IMGEN_DATA=<临时目录> 后台启动 `imgen serve`
      ③ 轮询 GET /api/v1/health，最多 30s
      ④ 校验 health / providers / settings / jobs / assets / usage / presets / 静态首页
      ⑤（除非 -SkipContract）在 web/ 下设 IMGEN_BASE 后跑 pnpm run check:contract
      ⑥ finally：停进程、删临时目录；失败时先打印日志尾部再清理

.PARAMETER Port
    监听端口，默认 18089（刻意避开默认 8080，避免撞上正在自用的实例）。

.PARAMETER DataDir
    测试数据目录。默认 .smoke-tmp\run-<时间戳>。**脚本结束会整个删除它**。

.PARAMETER SkipBuild
    跳过构建，直接使用仓库根目录已有的 imgen.exe。

.PARAMETER SkipContract
    跳过前端契约校验（pnpm run check:contract，需要 web/node_modules）。

.EXAMPLE
    pwsh -NoProfile -File scripts/smoke.ps1

.EXAMPLE
    pwsh -NoProfile -File scripts/smoke.ps1 -SkipBuild -SkipContract
#>
param(
    [int]$Port = 18089,
    [string]$DataDir = '',
    [switch]$SkipBuild,
    [switch]$SkipContract
)

$ErrorActionPreference = 'Stop'

# ============================================================
# 基础设施
# ============================================================

$script:Passed = 0
$script:Failed = 0
$script:FailureNames = [System.Collections.Generic.List[string]]::new()

# 仓库根 = 本脚本所在目录（scripts/）的上一级。
$repoRoot = Split-Path -Parent $PSScriptRoot
if (-not (Test-Path -LiteralPath (Join-Path $repoRoot 'go.mod'))) {
    throw "找不到仓库根（$repoRoot 下没有 go.mod），请在 Imgen 仓库内运行本脚本。"
}
Set-Location -LiteralPath $repoRoot

$script:BaseUrl = "http://127.0.0.1:$Port"

function Write-Section([string]$Name) {
    Write-Host ''
    Write-Host "[$Name]" -ForegroundColor Cyan
}

function Write-Ok([string]$Name, [string]$Detail = '') {
    $script:Passed++
    if ($Detail) { Write-Host "  ok   $Name  ($Detail)" -ForegroundColor Green }
    else { Write-Host "  ok   $Name" -ForegroundColor Green }
}

function Write-Fail([string]$Name, [string]$Detail = '') {
    $script:Failed++
    $script:FailureNames.Add($Name)
    if ($Detail) { Write-Host "  FAIL $Name  → $Detail" -ForegroundColor Red }
    else { Write-Host "  FAIL $Name" -ForegroundColor Red }
}

# Check：条件成立记 ok，否则记 FAIL。
function Check([string]$Name, [bool]$Condition, [string]$Detail = '') {
    if ($Condition) { Write-Ok $Name $Detail } else { Write-Fail $Name $Detail }
}

# Skip：不计入统计，只提示（用于 -SkipBuild / -SkipContract）。
function Write-Skip([string]$Name) {
    Write-Host "  skip $Name" -ForegroundColor DarkYellow
}

# HasProp：JSON 属性是否存在。用 PSObject.Properties 探测，
# 避免 ConvertFrom-Json 后访问不存在的属性抛错 / 静默为 $null。
function HasProp($Object, [string]$Name) {
    if ($null -eq $Object) { return $false }
    return ($null -ne $Object.PSObject.Properties[$Name])
}

# IsNumber：判断 ConvertFrom-Json 出来的值是不是数字。
# 注意 PowerShell 7 会把 JSON 整数解析成 Int64（不是 Int32），
# 所以不能用 `-is [int]` 判断，否则 2 会被判成「不是数字」。
function IsNumber($Value) {
    return ($Value -is [int] -or $Value -is [long] -or $Value -is [double] -or
            $Value -is [decimal] -or $Value -is [System.Int16] -or $Value -is [System.Byte])
}

# Invoke-Api：发 GET，返回 Status / Raw / Json（JSON 解析失败时 Json 为 $null）。
# 用 -SkipHttpErrorCheck 保证 4xx/5xx 也走同一路径，由 Check 判定。
function Invoke-Api([string]$Path, [int]$TimeoutSec = 10) {
    $r = Invoke-WebRequest -Uri ($script:BaseUrl + $Path) -TimeoutSec $TimeoutSec -SkipHttpErrorCheck
    $json = $null
    if ($r.Content) {
        try { $json = $r.Content | ConvertFrom-Json } catch { $json = $null }
    }
    [pscustomobject]@{ Status = [int]$r.StatusCode; Raw = [string]$r.Content; Json = $json }
}

# Resolve-Exe：优先取原生可执行（.CMD/.EXE），其次任意同名命令。
function Resolve-Exe([string]$Name) {
    $c = Get-Command $Name -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if (-not $c) { $c = Get-Command $Name -ErrorAction SilentlyContinue | Select-Object -First 1 }
    if (-not $c) { return $null }
    if ($c.Source) { return $c.Source }
    return $c.Path
}

# 端口占用探测：先尝试绑定回环地址，再查监听表（双保险，避免静默挂到旧进程上）。
function Get-PortOwner([int]$P) {
    $listener = $null
    try {
        $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, $P)
        $listener.Start()
        return $null   # 绑定成功 → 端口空闲
    } catch {
        # 落到下面的监听表查询
    } finally {
        if ($listener) { try { $listener.Stop() } catch { } }
    }
    $owner = $null
    try {
        $conn = Get-NetTCPConnection -LocalPort $P -State Listen -ErrorAction Stop | Select-Object -First 1
        if ($conn) { $owner = $conn.OwningProcess }
    } catch { }
    if ($null -eq $owner) { $owner = 'unknown' }
    return $owner
}

# 数据目录守卫：宁可拒绝，也不能碰到用户真实数据。
function Assert-SafeDataDir([string]$Path) {
    $sep = [System.IO.Path]::DirectorySeparatorChar
    $full = [System.IO.Path]::GetFullPath($Path).TrimEnd($sep)
    $realData = [System.IO.Path]::GetFullPath((Join-Path $repoRoot 'data')).TrimEnd($sep)
    $root = [System.IO.Path]::GetFullPath($repoRoot).TrimEnd($sep)

    if ($full -eq $realData -or $full.StartsWith($realData + $sep, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "拒绝使用真实数据目录：$full（本机 ./data 是用户真实数据，绝不能当测试目录）"
    }
    if ($full -eq $root) {
        throw "拒绝以仓库根作为数据目录：$full"
    }
    foreach ($f in @('imgen.db', 'master.key')) {
        if (Test-Path -LiteralPath (Join-Path $full $f)) {
            throw "数据目录 $full 里已有 $f —— 那是真实数据，不能拿来当测试目录"
        }
    }
}

function Write-LogTail([string]$Path, [string]$Label, [int]$Lines = 40) {
    if (-not (Test-Path -LiteralPath $Path)) { return }
    Write-Host "---- $Label（末尾 $Lines 行）----" -ForegroundColor DarkYellow
    Get-Content -LiteralPath $Path -Tail $Lines -ErrorAction SilentlyContinue |
        ForEach-Object { Write-Host "    $_" }
}

# ============================================================
# 初始化：临时目录 / 端口 / 环境变量备份
# ============================================================

if ($Port -lt 1 -or $Port -gt 65535) {
    Write-Host "FAIL 端口非法：$Port（应在 1..65535）" -ForegroundColor Red
    Write-Host ''
    Write-Host ' 汇总：通过 0 项 / 失败 1 项 ✗' -ForegroundColor Red
    exit 1
}

$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
if ([string]::IsNullOrWhiteSpace($DataDir)) {
    $tempPath = Join-Path $repoRoot (Join-Path '.smoke-tmp' "run-$stamp")
    # 默认路径由脚本自己建，结束时也由脚本删。
    $script:OwnsTempDir = $true
} else {
    $tempPath = $DataDir
    # 显式指定时只在「调用前不存在」的情况下才归我们所有；
    # 已存在的目录可能装着别人的东西，绝不自动删。
    $script:OwnsTempDir = -not (Test-Path -LiteralPath $DataDir)
}
$tempPath = [System.IO.Path]::GetFullPath($tempPath)

# 守卫必须在创建任何目录之前跑一遍（默认路径一定安全，但仍统一走同一检查）。
# 这里是「还没开始测」的硬失败：不进 try/finally，也不碰任何目录。
try {
    Assert-SafeDataDir $tempPath
} catch {
    Write-Host "FAIL 测试数据目录不安全 → $($_.Exception.Message)" -ForegroundColor Red
    Write-Host ''
    Write-Host ' 汇总：通过 0 项 / 失败 1 项 ✗' -ForegroundColor Red
    exit 1
}

$envBackup = @{
    IMGEN_ADDR = $env:IMGEN_ADDR
    IMGEN_DATA = $env:IMGEN_DATA
    IMGEN_BASE = $env:IMGEN_BASE
}

$proc = $null
$logOut = Join-Path $tempPath 'server.out.log'
$logErr = Join-Path $tempPath 'server.err.log'

Write-Host '============================================================' -ForegroundColor White
Write-Host ' Imgen 冒烟测试' -ForegroundColor White
Write-Host '============================================================' -ForegroundColor White
Write-Host "  仓库根   : $repoRoot"
Write-Host "  监听     : 127.0.0.1:$Port"
Write-Host "  测试数据 : $tempPath"
Write-Host '  （不是 ./data —— 本机真实数据绝不会被读写）' -ForegroundColor DarkGray

try {
    # ---------- 端口占用 ----------
    Write-Section 'preflight'
    $owner = Get-PortOwner $Port
    if ($null -ne $owner) {
        Check "端口 $Port 空闲" $false "已被占用（PID: $owner）。换端口：-Port <port>，或先停掉占用进程。" 
        throw 'SMOKE_ABORT: 端口被占用'
    }
    Write-Ok "端口 $Port 空闲"

    # ---------- 临时数据目录 ----------
    #
    # 安全规则：只允许「本来就不存在」或「本来为空」的目录。
    # - 默认路径（.smoke-tmp\run-<时间戳>）一定不存在，除非同一秒重复运行。
    # - 显式 -DataDir 指向一个非空目录时**直接拒绝**，绝不替用户清空它。
    if (Test-Path -LiteralPath $tempPath) {
        if ($script:OwnsTempDir) {
            # 同一秒内的重复运行留下的旧目录（守卫已确认里面没有 imgen.db / master.key）。
            Remove-Item -LiteralPath $tempPath -Recurse -Force -ErrorAction SilentlyContinue
        } else {
            $existing = @(Get-ChildItem -LiteralPath $tempPath -Force -ErrorAction SilentlyContinue)
            if ($existing.Count -gt 0) {
                Check '测试数据目录可用' $false `
                    "$tempPath 已存在且非空（$($existing.Count) 项）。本脚本不会清理已有目录，请换一个空目录，或去掉 -DataDir 用默认临时目录。"
                throw 'SMOKE_ABORT: 数据目录非空'
            }
            Write-Ok '使用已存在的空目录' $tempPath
        }
    }
    New-Item -ItemType Directory -Path $tempPath -Force | Out-Null
    Write-Ok '临时数据目录已就绪' $tempPath

    # ---------- ① 构建 ----------
    if ($SkipBuild) {
        $exePath = Join-Path $repoRoot 'imgen.exe'
        if (-not (Test-Path -LiteralPath $exePath)) {
            Check 'imgen.exe 存在（-SkipBuild）' $false "找不到 $exePath；去掉 -SkipBuild 让脚本自己构建（go build -tags imgenweb -o ... ./cmd/imgen）"
            throw 'SMOKE_ABORT: 缺少构建产物'
        }
        Write-Skip '构建（-SkipBuild，使用仓库根 imgen.exe）'
        Write-Ok '使用已有二进制' $exePath
    } else {
        $exePath = Join-Path $tempPath 'imgen.exe'
        $goExe = Resolve-Exe 'go'
        if (-not $goExe) {
            Check 'go 可执行文件在 PATH' $false '未找到 go'
            throw 'SMOKE_ABORT: 缺少 go'
        }
        Write-Host "==> go build -tags imgenweb -o $exePath ./cmd/imgen" -ForegroundColor DarkGray
        & $goExe build -tags imgenweb -o $exePath ./cmd/imgen
        $buildCode = $LASTEXITCODE
        Check 'go build（-tags imgenweb）' ($buildCode -eq 0) "exit=$buildCode"
        if ($buildCode -ne 0) { throw 'SMOKE_ABORT: 构建失败' }
    }

    # ---------- ② 启动服务 ----------
    Write-Section 'serve'
    $env:IMGEN_ADDR = "127.0.0.1:$Port"
    $env:IMGEN_DATA = $tempPath
    # 日志重定向到文件：失败时 finally 里打尾部，便于排障。
    $proc = Start-Process -FilePath $exePath -ArgumentList 'serve' -PassThru -WindowStyle Hidden `
        -RedirectStandardOutput $logOut -RedirectStandardError $logErr
    if (-not $proc) {
        Check 'imgen serve 启动' $false 'Start-Process 未返回进程'
        throw 'SMOKE_ABORT: 启动失败'
    }
    Write-Ok 'imgen serve 已启动' "PID $($proc.Id)"

    # ---------- ③ 轮询 health（最多 30s） ----------
    $deadline = (Get-Date).AddSeconds(30)
    $ready = $false
    $lastErr = ''
    while ((Get-Date) -lt $deadline) {
        if ($proc.HasExited) { break }
        try {
            $h = Invoke-RestMethod -Uri ($script:BaseUrl + '/api/v1/health') -TimeoutSec 2
            if ($h.ok -eq $true) { $ready = $true; break }
        } catch {
            $lastErr = $_.Exception.Message
        }
        Start-Sleep -Milliseconds 300
    }

    if (-not $ready) {
        $why = if ($proc.HasExited) { "进程已退出（exit=$($proc.ExitCode)）" } elseif ($lastErr) { $lastErr } else { '超时' }
        Check 'GET /api/v1/health 在 30s 内就绪' $false $why
        throw 'SMOKE_ABORT: 服务未就绪'
    }
    Write-Ok 'GET /api/v1/health 在 30s 内就绪'

    # ---------- ④ 接口形状校验 ----------
    Write-Section 'health'
    $health = Invoke-Api '/api/v1/health'
    Check 'health HTTP 200' ($health.Status -eq 200) "status=$($health.Status)"
    Check 'health.ok === true' ((HasProp $health.Json 'ok') -and ($health.Json.ok -eq $true))
    Check 'health.api === "v1"' ((HasProp $health.Json 'api') -and ($health.Json.api -eq 'v1')) "$($health.Json.api)"

    Write-Section 'providers'
    $provs = Invoke-Api '/api/v1/providers'
    Check 'providers HTTP 200' ($provs.Status -eq 200) "status=$($provs.Status)"
    Check '响应是 {providers:[...]} 信封（非裸数组）' ((HasProp $provs.Json 'providers') -and ($provs.Json.providers -is [System.Array]))
    $provIds = @()
    if (HasProp $provs.Json 'providers') { $provIds = @($provs.Json.providers | ForEach-Object { $_.id }) }
    Check '两家通道都在（sensenova / agnes）' `
        (($provIds -contains 'sensenova') -and ($provIds -contains 'agnes')) `
        "ids=$(($provIds) -join ',')"
    $firstProv = @($provs.Json.providers) | Select-Object -First 1
    Check '每个 provider 都带 models 与 caps' `
        ((HasProp $firstProv 'models') -and ($firstProv.models -is [System.Array]) -and
         (HasProp $firstProv 'caps') -and ($null -ne $firstProv.caps))

    Write-Section 'settings'
    $settings = Invoke-Api '/api/v1/settings'
    Check 'settings HTTP 200' ($settings.Status -eq 200) "status=$($settings.Status)"
    Check 'GET /settings 是裸对象（无 settings 信封）' (-not (HasProp $settings.Json 'settings'))
    Check '含 concurrency / maxUploadMB / keepDays' `
        ((HasProp $settings.Json 'concurrency') -and (HasProp $settings.Json 'maxUploadMB') -and (HasProp $settings.Json 'keepDays')) `
        "concurrency=$($settings.Json.concurrency) maxUploadMB=$($settings.Json.maxUploadMB) keepDays=$($settings.Json.keepDays)"
    Check 'concurrency 是数字且在 1..16' `
        ((IsNumber $settings.Json.concurrency) -and ($settings.Json.concurrency -ge 1) -and ($settings.Json.concurrency -le 16)) `
        "$($settings.Json.concurrency)"
    Check 'keepDays 是数字（0 = 关闭清理）' ((IsNumber $settings.Json.keepDays) -and ($settings.Json.keepDays -ge 0)) "$($settings.Json.keepDays)"

    Write-Section 'jobs / assets'
    foreach ($p in @('/api/v1/jobs', '/api/v1/assets')) {
        $r = Invoke-Api $p
        $shapeOk = (HasProp $r.Json 'items') -and (HasProp $r.Json 'total') -and
                   (HasProp $r.Json 'limit') -and (HasProp $r.Json 'offset') -and
                   ($r.Json.items -is [System.Array])
        Check "GET $p 是 {items,total,limit,offset}" (($r.Status -eq 200) -and $shapeOk) `
            "status=$($r.Status) total=$($r.Json.total) limit=$($r.Json.limit) offset=$($r.Json.offset)"
    }

    Write-Section 'usage'
    $usage = Invoke-Api '/api/v1/usage'
    Check 'GET /api/v1/usage 是 {days,items}' `
        (($usage.Status -eq 200) -and (HasProp $usage.Json 'days') -and (HasProp $usage.Json 'items') -and ($usage.Json.items -is [System.Array])) `
        "status=$($usage.Status) days=$($usage.Json.days)"

    Write-Section 'presets'
    $presets = Invoke-Api '/api/v1/presets'
    Check 'GET /api/v1/presets 是 {presets:[...]} 信封' `
        (($presets.Status -eq 200) -and (HasProp $presets.Json 'presets') -and ($presets.Json.presets -is [System.Array])) `
        "status=$($presets.Status)"
    $presetIds = @()
    if (HasProp $presets.Json 'presets') { $presetIds = @($presets.Json.presets | ForEach-Object { $_.id }) }
    Check '内置预设 p_builtin_poster_split 存在' ($presetIds -contains 'p_builtin_poster_split') `
        "ids=$(($presetIds) -join ',')"

    Write-Section 'static'
    $index = Invoke-WebRequest -Uri ($script:BaseUrl + '/') -TimeoutSec 10 -SkipHttpErrorCheck
    $ctype = [string]($index.Headers['Content-Type'] -join ',')
    $html = [string]$index.Content
    Check 'GET / 返回 200 HTML' `
        (([int]$index.StatusCode -eq 200) -and ($ctype -match 'text/html')) `
        "status=$([int]$index.StatusCode) content-type=$ctype"
    Check 'HTML 注入 window.__IMGEN__ 启动配置' ($html -like '*window.__IMGEN__*')
    if ($html -like '*前端产物尚未构建*') {
        Check '前端是真实产物（非 dist_stub 占位页）' $false '二进制未带 -tags imgenweb，或 internal/web/dist 未构建（pnpm --dir web build）'
    } else {
        Write-Ok '前端是真实产物（非 dist_stub 占位页）'
    }

    # ---------- ⑤ 前端契约校验 ----------
    Write-Section 'contract'
    if ($SkipContract) {
        Write-Skip 'pnpm run check:contract（-SkipContract）'
    } else {
        $pnpmExe = Resolve-Exe 'pnpm'
        if (-not $pnpmExe) {
            Check 'pnpm 在 PATH（契约校验需要）' $false '未找到 pnpm；可用 -SkipContract 跳过'
        } else {
            if (-not (Test-Path -LiteralPath (Join-Path $repoRoot 'web\node_modules'))) {
                Write-Host '  (web/node_modules 不存在，pnpm 会提示安装；必要时 pnpm --dir web install)' -ForegroundColor DarkGray
            }
            # check-contract.ts 读的是 IMGEN_BASE（不是 IMGEN_BASE_URL）。
            $env:IMGEN_BASE = $script:BaseUrl
            Push-Location -LiteralPath (Join-Path $repoRoot 'web')
            try {
                & $pnpmExe run check:contract
                $contractCode = $LASTEXITCODE
            } finally {
                Pop-Location
            }
            Check 'pnpm run check:contract 全绿' ($contractCode -eq 0) "exit=$contractCode"
        }
    }
}
catch {
    if ($_.Exception.Message -like 'SMOKE_ABORT:*') {
        # 具体失败项已在上面记录，这里不重复计数。
        Write-Host "  （提前终止：$($_.Exception.Message)）" -ForegroundColor DarkYellow
    } else {
        Write-Fail '未捕获异常' $_.Exception.Message
        Write-Host $_.ScriptStackTrace -ForegroundColor DarkGray
    }
}
finally {
    # 失败时先打日志尾部（进程还活着也能读已 flush 的内容），再清理。
    if ($script:Failed -gt 0) {
        Write-Section 'server logs'
        Write-LogTail $logOut 'stdout'
        Write-LogTail $logErr 'stderr'
    }

    # ⑥ 停进程
    if ($proc) {
        try {
            if (-not $proc.HasExited) {
                Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
                $proc.WaitForExit(5000) | Out-Null
            }
        } catch { }
    }

    # 恢复环境变量（避免影响同一进程里的后续步骤）
    foreach ($k in $envBackup.Keys) {
        if ($null -eq $envBackup[$k]) { Remove-Item -Path "Env:$k" -ErrorAction SilentlyContinue }
        else { Set-Item -Path "Env:$k" -Value $envBackup[$k] }
    }

    # 清理临时目录（SQLite/文件句柄可能晚一拍释放，重试几次）。
    # 只自动删「我们自己建的」目录：默认 .smoke-tmp 下的路径，或调用前不存在的目录。
    # 用户显式传了一个非空目录时只警告，不替他删东西。
    if ($tempPath -and (Test-Path -LiteralPath $tempPath)) {
        if ($script:OwnsTempDir) {
            for ($i = 0; $i -lt 5; $i++) {
                try {
                    Remove-Item -LiteralPath $tempPath -Recurse -Force -ErrorAction Stop
                    break
                } catch {
                    Start-Sleep -Milliseconds 300
                }
            }
            # 顺手收掉空的 .smoke-tmp 父目录，别在仓库里留空壳。
            $parent = Split-Path -Parent $tempPath
            if ($parent -and (Test-Path -LiteralPath $parent)) {
                $rest = @(Get-ChildItem -LiteralPath $parent -Force -ErrorAction SilentlyContinue)
                if ($rest.Count -eq 0) {
                    Remove-Item -LiteralPath $parent -Force -ErrorAction SilentlyContinue
                }
            }
        }
        if (Test-Path -LiteralPath $tempPath) {
            if ($script:OwnsTempDir) {
                Write-Host "  警告：临时目录未能完全清理，请手动删除：$tempPath" -ForegroundColor DarkYellow
            } else {
                Write-Host "  提示：-DataDir 指定的目录未自动删除，如需清理请手动删：$tempPath" -ForegroundColor DarkYellow
            }
        }
    }
}

# ============================================================
# 汇总
# ============================================================
Write-Host ''
Write-Host '============================================================' -ForegroundColor White
if ($script:Failed -eq 0) {
    Write-Host " 汇总：通过 $($script:Passed) 项 / 失败 0 项 —— 全绿 ✓" -ForegroundColor Green
    exit 0
}
Write-Host " 汇总：通过 $($script:Passed) 项 / 失败 $($script:Failed) 项 ✗" -ForegroundColor Red
foreach ($n in $script:FailureNames) { Write-Host "   - $n" -ForegroundColor Red }
exit 1
