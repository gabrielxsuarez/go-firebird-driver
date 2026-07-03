param(
    [ValidateSet("quick", "full", "race", "fuzz", "bench", "all")]
    [string]$Mode = "quick",
    [string]$FB3TestDsn = $env:FB3_TEST_DSN,
    [int]$FuzzSeconds = 10,
    [int]$BenchCount = 3,
    [switch]$SkipIntegration
)

$ErrorActionPreference = "Stop"

$RepoRoot = Resolve-Path -LiteralPath (Join-Path $PSScriptRoot "..")
Set-Location -LiteralPath $RepoRoot

function Invoke-Step {
    param(
        [string]$Name,
        [scriptblock]$Command
    )
    Write-Host ""
    Write-Host "==> $Name" -ForegroundColor Cyan
    & $Command
    if ($LASTEXITCODE -ne 0) {
        throw "Step failed: $Name"
    }
}

if ($FB3TestDsn) {
    $env:FB3_TEST_DSN = $FB3TestDsn
}

$hasIntegration = -not $SkipIntegration -and [bool]$env:FB3_TEST_DSN
if (-not $hasIntegration) {
    Write-Host "FB3_TEST_DSN is not set; integration tests that need FB3 will be skipped by this script." -ForegroundColor Yellow
}

function Invoke-Unit {
    Invoke-Step "Unit and wire tests" {
        go test ./internal/... ./internal/wire/...
    }
    Invoke-Step "DSN and local driver contract tests" {
        go test -run "TestParseDSN|TestHandle|TestWithCancel|TestBuildConnectDPB" .
    }
}

function Invoke-Integration {
    if (-not $hasIntegration) {
        return
    }
    Invoke-Step "Integration tests" {
        go test -count=1 ./...
    }
}

function Invoke-Race {
    if (-not $hasIntegration) {
        Invoke-Step "Race tests without integration DSN" {
            go test -race ./internal/... ./internal/wire/...
        }
        return
    }
    Invoke-Step "Race tests" {
        go test -race ./...
    }
}

function Invoke-Fuzz {
    Invoke-Step "Fuzz ParseDSN" {
        go test . "-run=^$" -fuzz=FuzzParseDSN "-fuzztime=$($FuzzSeconds)s"
    }
    Invoke-Step "Fuzz ParseInfoBuffer" {
        go test ./internal/wire "-run=^$" -fuzz=FuzzParseInfoBuffer "-fuzztime=$($FuzzSeconds)s"
    }
    Invoke-Step "Fuzz ParseSQLDescribeInfo" {
        go test ./internal/wire "-run=^$" -fuzz=FuzzParseSQLDescribeInfo "-fuzztime=$($FuzzSeconds)s"
    }
    Invoke-Step "Fuzz ParseRecordCounts" {
        go test ./internal/wire "-run=^$" -fuzz=FuzzParseRecordCounts "-fuzztime=$($FuzzSeconds)s"
    }
}

function Invoke-Bench {
    Invoke-Step "Wire benchmarks" {
        go test ./internal/wire "-run=^$" -bench="^(BenchmarkRead|BenchmarkWrite|BenchmarkArc4|BenchmarkChaCha|BenchmarkCrypt|BenchmarkEncodeParams|BenchmarkEstimate|BenchmarkStackWriter|BenchmarkToString|BenchmarkRepeatZeros|BenchmarkScaledInt)" -benchmem "-count=$BenchCount"
    }
    Invoke-Step "Charset benchmarks" {
        go test ./internal/charset "-run=^$" "-bench=." -benchmem "-count=$BenchCount"
    }
    if ($hasIntegration) {
        Invoke-Step "Driver integration benchmarks" {
            go test . "-run=^$" -bench="^Benchmark(Ping|QuerySingleRow|ExecInsert|PreparedExec|QueryManyRows)$" -benchmem "-count=$BenchCount"
        }
    }
}

switch ($Mode) {
    "quick" {
        Invoke-Unit
        Invoke-Integration
    }
    "full" {
        Invoke-Unit
        Invoke-Integration
        Invoke-Fuzz
    }
    "race" {
        Invoke-Race
    }
    "fuzz" {
        Invoke-Fuzz
    }
    "bench" {
        Invoke-Bench
    }
    "all" {
        Invoke-Unit
        Invoke-Integration
        Invoke-Race
        Invoke-Fuzz
        Invoke-Bench
    }
}
