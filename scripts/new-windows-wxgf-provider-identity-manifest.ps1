<#
.SYNOPSIS
Creates an adjacent identity manifest for the qualification-only WXGF provider.

.DESCRIPTION
The manifest records operator-selected provider and adjacent FFmpeg SHA-256
values. It is an unsigned identity intent, not proof of source, signature, or
distribution-license compliance. The host independently stages and hashes the
exact files before running them. This script performs no network access.
#>
[CmdletBinding()]
param(
    [string]$Provider,
    [string]$Decoder,
    [switch]$ShowPaths,
    [switch]$SelfTest
)

Set-StrictMode -Version 3.0

$ManifestProtocol = 'v-local-cli/wxgf-provider-identity-manifest/v1'
$MaximumProviderBytes = 256MB
$MaximumDecoderBytes = 1GB

function Stop-Manifest {
    param([Parameter(Mandatory = $true)][string]$Code)
    throw [System.InvalidOperationException]::new("wxgf-provider-manifest:$Code")
}

function Assert-LocalAbsolutePath {
    param([Parameter(Mandatory = $true)][string]$Path)
    if (-not [System.IO.Path]::IsPathFullyQualified($Path) -or $Path.StartsWith('\\') -or $Path.StartsWith('//')) {
        Stop-Manifest 'path_not_local_absolute'
    }
}

function Assert-NoReparsePoint {
    param([Parameter(Mandatory = $true)][string]$Path)
    $FullPath = [System.IO.Path]::GetFullPath($Path)
    $Root = [System.IO.Path]::GetPathRoot($FullPath)
    $Current = $Root
    $Remainder = $FullPath.Substring($Root.Length)
    foreach ($Segment in $Remainder.Split([System.IO.Path]::DirectorySeparatorChar, [System.StringSplitOptions]::RemoveEmptyEntries)) {
        $Current = Join-Path $Current $Segment
        $Item = Get-Item -LiteralPath $Current -Force -ErrorAction Stop
        if (($Item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
            Stop-Manifest 'path_contains_reparse_point'
        }
    }
}

function Resolve-RegularFile {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][long]$MaximumBytes,
        [Parameter(Mandatory = $true)][string]$Code
    )
    Assert-LocalAbsolutePath $Path
    Assert-NoReparsePoint $Path
    $Item = Get-Item -LiteralPath $Path -Force -ErrorAction Stop
    if ($Item.PSIsContainer -or (($Item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) -or
        $Item.Length -le 0 -or $Item.Length -gt $MaximumBytes) {
        Stop-Manifest $Code
    }
    return [System.IO.Path]::GetFullPath($Item.FullName)
}

function Get-StableSHA256 {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][long]$MaximumBytes
    )
    $Before = Get-Item -LiteralPath $Path -Force -ErrorAction Stop
    $Stream = [System.IO.FileStream]::new(
        $Path,
        [System.IO.FileMode]::Open,
        [System.IO.FileAccess]::Read,
        [System.IO.FileShare]::Read
    )
    try {
        if ($Stream.Length -le 0 -or $Stream.Length -gt $MaximumBytes) {
            Stop-Manifest 'file_size_invalid'
        }
        $Digest = [Convert]::ToHexString(
            [System.Security.Cryptography.SHA256]::HashData($Stream)
        ).ToLowerInvariant()
    }
    finally {
        $Stream.Dispose()
    }
    $After = Get-Item -LiteralPath $Path -Force -ErrorAction Stop
    if ($Before.Length -ne $After.Length -or $Before.LastWriteTimeUtc -ne $After.LastWriteTimeUtc) {
        Stop-Manifest 'file_changed_while_hashing'
    }
    return $Digest
}

function Write-ExclusiveUTF8 {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Text
    )
    $Encoding = [System.Text.UTF8Encoding]::new($false)
    $Bytes = $Encoding.GetBytes($Text + [Environment]::NewLine)
    $Stream = [System.IO.FileStream]::new(
        $Path,
        [System.IO.FileMode]::CreateNew,
        [System.IO.FileAccess]::Write,
        [System.IO.FileShare]::None
    )
    $Succeeded = $false
    try {
        $Stream.Write($Bytes, 0, $Bytes.Length)
        $Stream.Flush($true)
        $Succeeded = $true
    }
    finally {
        $Stream.Dispose()
        if (-not $Succeeded -and (Test-Path -LiteralPath $Path)) {
            Remove-Item -LiteralPath $Path -Force -ErrorAction SilentlyContinue
        }
    }
}

function New-IdentityManifest {
    param(
        [Parameter(Mandatory = $true)][string]$ProviderPath,
        [Parameter(Mandatory = $true)][string]$DecoderPath
    )
    $ProviderPath = Resolve-RegularFile $ProviderPath $MaximumProviderBytes 'provider_invalid'
    $DecoderPath = Resolve-RegularFile $DecoderPath $MaximumDecoderBytes 'decoder_invalid'
    $ProviderDirectory = [System.IO.Path]::GetDirectoryName($ProviderPath)
    $DecoderDirectory = [System.IO.Path]::GetDirectoryName($DecoderPath)
    if (-not $ProviderDirectory.Equals($DecoderDirectory, [System.StringComparison]::OrdinalIgnoreCase)) {
        Stop-Manifest 'provider_decoder_not_adjacent'
    }
    if ($ProviderPath.Equals($DecoderPath, [System.StringComparison]::OrdinalIgnoreCase)) {
        Stop-Manifest 'provider_decoder_same_file'
    }
    if ([System.IO.Path]::GetFileName($DecoderPath) -ine 'ffmpeg.exe') {
        Stop-Manifest 'decoder_file_name_invalid'
    }
    $ManifestPath = $ProviderPath + '.manifest.json'
    if (Test-Path -LiteralPath $ManifestPath) {
        Stop-Manifest 'manifest_exists'
    }
    $ProviderSHA256 = Get-StableSHA256 $ProviderPath $MaximumProviderBytes
    $DecoderSHA256 = Get-StableSHA256 $DecoderPath $MaximumDecoderBytes
    $Manifest = [ordered]@{
        protocol = $ManifestProtocol
        provider_file_name = [System.IO.Path]::GetFileName($ProviderPath)
        provider_sha256 = $ProviderSHA256
        decoder_name = 'ffmpeg'
        decoder_file_name = 'ffmpeg.exe'
        decoder_sha256 = $DecoderSHA256
        provider_source_status = 'unverified'
        decoder_source_status = 'unverified'
        decoder_distribution_license_status = 'not_qualified'
    }
    Write-ExclusiveUTF8 $ManifestPath ($Manifest | ConvertTo-Json -Compress)
    return [pscustomobject]@{
        path = [System.IO.Path]::GetFullPath($ManifestPath)
        provider_sha256 = $ProviderSHA256
        decoder_sha256 = $DecoderSHA256
    }
}

function Invoke-SelfTest {
    $Root = Join-Path ([System.IO.Path]::GetTempPath()) ('v-local-wxgf-provider-manifest-self-test-' + [Guid]::NewGuid().ToString('N'))
    $ProviderPath = Join-Path $Root 'provider.exe'
    $DecoderPath = Join-Path $Root 'ffmpeg.exe'
    $ManifestPath = $ProviderPath + '.manifest.json'
    $SameFileManifestPath = $DecoderPath + '.manifest.json'
    try {
        [void](New-Item -ItemType Directory -Path $Root -ErrorAction Stop)
        [System.IO.File]::WriteAllBytes($ProviderPath, [byte[]](1, 2, 3, 4))
        [System.IO.File]::WriteAllBytes($DecoderPath, [byte[]](5, 6, 7, 8))
        $Result = New-IdentityManifest $ProviderPath $DecoderPath
        $Value = Get-Content -LiteralPath $ManifestPath -Raw -ErrorAction Stop | ConvertFrom-Json -ErrorAction Stop
        if ($Value.protocol -cne $ManifestProtocol -or $Value.provider_sha256 -cne $Result.provider_sha256 -or
            $Value.decoder_sha256 -cne $Result.decoder_sha256 -or $Value.provider_source_status -cne 'unverified' -or
            $Value.decoder_distribution_license_status -cne 'not_qualified') {
            Stop-Manifest 'self_test_manifest_invalid'
        }
        $OverwriteRejected = $false
        try {
            [void](New-IdentityManifest $ProviderPath $DecoderPath)
        }
        catch {
            $OverwriteRejected = ([string]$_.Exception.Message -ceq 'wxgf-provider-manifest:manifest_exists')
        }
        if (-not $OverwriteRejected) {
            Stop-Manifest 'self_test_overwrite_not_rejected'
        }
        $SameFileRejected = $false
        try {
            [void](New-IdentityManifest $DecoderPath $DecoderPath)
        }
        catch {
            $SameFileRejected = ([string]$_.Exception.Message -ceq 'wxgf-provider-manifest:provider_decoder_same_file')
        }
        if (-not $SameFileRejected) {
            Stop-Manifest 'self_test_same_file_not_rejected'
        }
        $ForwardUNCRejected = $false
        try {
            Assert-LocalAbsolutePath '//server/share/provider.exe'
        }
        catch {
            $ForwardUNCRejected = ([string]$_.Exception.Message -ceq 'wxgf-provider-manifest:path_not_local_absolute')
        }
        if (-not $ForwardUNCRejected) {
            Stop-Manifest 'self_test_forward_unc_not_rejected'
        }
    }
    finally {
        foreach ($Path in @($SameFileManifestPath, $ManifestPath, $DecoderPath, $ProviderPath)) {
            if (Test-Path -LiteralPath $Path) {
                Remove-Item -LiteralPath $Path -Force -ErrorAction SilentlyContinue
            }
        }
        if (Test-Path -LiteralPath $Root) {
            $Remaining = @(Get-ChildItem -LiteralPath $Root -Force -ErrorAction SilentlyContinue)
            if ($Remaining.Count -eq 0) {
                Remove-Item -LiteralPath $Root -Force -ErrorAction SilentlyContinue
            }
        }
    }
    if (Test-Path -LiteralPath $Root) {
        Stop-Manifest 'self_test_cleanup_failed'
    }
    [ordered]@{
        self_test = 'passed'
        manifest_protocol = $ManifestProtocol
        host_rehash_required = $true
        proves_provenance = $false
        qualifies_distribution_license = $false
        network = $false
    } | ConvertTo-Json -Compress
}

if ($SelfTest) {
    Invoke-SelfTest
    exit 0
}

try {
    if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
        Stop-Manifest 'windows_required'
    }
    if ($PSVersionTable.PSVersion.Major -lt 7) {
        Stop-Manifest 'powershell_7_required'
    }
    $Result = New-IdentityManifest $Provider $Decoder
    $Output = [ordered]@{
        status = 'created'
        protocol = $ManifestProtocol
        manifest_path_included = [bool]$ShowPaths
        provider_source_status = 'unverified'
        decoder_source_status = 'unverified'
        decoder_distribution_license_status = 'not_qualified'
        network = $false
    }
    if ($ShowPaths) {
        $Output.manifest_path = $Result.path
    }
    $Output | ConvertTo-Json -Compress
    exit 0
}
catch {
    $Message = [string]$_.Exception.Message
    $Code = if ($Message.StartsWith('wxgf-provider-manifest:', [System.StringComparison]::Ordinal)) {
        $Message.Substring('wxgf-provider-manifest:'.Length)
    }
    else {
        'unexpected_failure'
    }
    [ordered]@{ status = 'failed'; failure_code = $Code; manifest_path_included = $false } |
        ConvertTo-Json -Compress
    exit 1
}
