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
$SigningCertificate = Get-Item -LiteralPath "Cert:\CurrentUser\My\$CertificateThumbprint" -ErrorAction Stop
if (-not $SigningCertificate.RawData) {
    throw '无法读取 Authenticode 签名证书'
}
$ReleaseSignerSHA256 = [Convert]::ToHexString(
    [Security.Cryptography.SHA256]::HashData($SigningCertificate.RawData)
).ToLowerInvariant()

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
    $LdFlags = "-s -w -X github.com/zanescope/v-local-cli/internal/provider.buildMode=release " +
        "-X github.com/zanescope/v-local-cli/internal/provider.releaseSignerSHA256=$ReleaseSignerSHA256"
    & go build -trimpath -ldflags $LdFlags -o $Output ./cmd/v-local-cli
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
$ActualSignerSHA256 = [Convert]::ToHexString(
    [Security.Cryptography.SHA256]::HashData($Signature.SignerCertificate.RawData)
).ToLowerInvariant()
if ($ActualSignerSHA256 -ne $ReleaseSignerSHA256) {
    throw '发布件签名者与编译期绑定证书不匹配'
}

$Digest = (Get-FileHash -Algorithm SHA256 -LiteralPath $Output).Hash.ToLowerInvariant()
$Manifest = [ordered]@{
    schema_version = 1
    platform = 'windows'
    arch = $Arch
    build_mode = 'release'
    runtime_authenticode_required = $true
    fixed_install_required = $true
    signature = 'authenticode'
    signer_thumbprint = $Signature.SignerCertificate.Thumbprint.ToLowerInvariant()
    signer_certificate_sha256 = $ReleaseSignerSHA256
    timestamp_signer_thumbprint = $Signature.TimeStamperCertificate.Thumbprint.ToLowerInvariant()
    sha256 = $Digest
    signed = $true
    timestamped = $true
} | ConvertTo-Json
[System.IO.File]::WriteAllText(
    (Join-Path $OutputDirectory 'signature-manifest.json'),
    $Manifest,
    [System.Text.UTF8Encoding]::new($false)
)

[ordered]@{
    target = "windows/$Arch"
    output = $Output
    sha256 = $Digest
    signed = $true
    timestamped = $true
} | ConvertTo-Json
