param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('amd64', 'arm64')]
    [string]$Arch,
    [Parameter(Mandatory = $true)]
    [string]$CertificateThumbprint,
    [string]$SignToolPath,
    [ValidatePattern('^https://')]
    [string]$TimestampUrl = 'https://timestamp.digicert.com'
)

$ErrorActionPreference = 'Stop'
$ProjectRoot = Split-Path -Parent $PSScriptRoot
$OutputDirectory = Join-Path $ProjectRoot "build\windows-$Arch"
$Output = Join-Path $OutputDirectory 'v-local-cli.exe'

if ($SignToolPath) {
    $ResolvedSignTool = (Resolve-Path -LiteralPath $SignToolPath -ErrorAction Stop).Path
}
else {
    $SignTool = Get-Command signtool.exe -ErrorAction SilentlyContinue
    if (-not $SignTool) { throw '未找到 SignTool；请安装 Windows SDK 或传入 -SignToolPath' }
    $ResolvedSignTool = $SignTool.Source
}

New-Item -ItemType Directory -Force $OutputDirectory | Out-Null
$env:GOOS = 'windows'
$env:GOARCH = $Arch
$env:CGO_ENABLED = '0'

Push-Location $ProjectRoot
try {
    & go build -trimpath -ldflags '-s -w' -o $Output ./cmd/v-local-cli
    if ($LASTEXITCODE -ne 0) { throw 'Windows 发布件构建失败' }
}
finally {
    Pop-Location
}

& $ResolvedSignTool sign /fd SHA256 /sha1 $CertificateThumbprint /tr $TimestampUrl /td SHA256 $Output
if ($LASTEXITCODE -ne 0) { throw 'Authenticode 签名失败' }
& $ResolvedSignTool verify /pa /all $Output
if ($LASTEXITCODE -ne 0) { throw 'Authenticode 验证失败' }
$Signature = Get-AuthenticodeSignature -LiteralPath $Output
if ($Signature.Status -ne 'Valid' -or -not $Signature.TimeStamperCertificate) {
    throw 'Windows 发布件缺少有效签名或可信时间戳'
}

[ordered]@{
    target = "windows/$Arch"
    output = $Output
    sha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $Output).Hash.ToLowerInvariant()
    signed = $true
    timestamped = $true
} | ConvertTo-Json
