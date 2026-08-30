<#
.SYNOPSIS
Classifies a current Windows Weixin xlog by frame structure without decoding log content.

.DESCRIPTION
Reads only Mars frame headers and tail markers from one xlog under the current user's
Tencent\xwechat\log directory. It reports aggregate format/encryption counts and never
outputs payload bytes, embedded public keys, fingerprints, paths, URLs, tokens, or log text.

This is a negative qualification aid. A structurally valid encrypted log requires the
matching application private key (or a vendor-matched decoder) before its plaintext can be
used as evidence. Successful inspection does not qualify a chat CDN endpoint or protocol.

.NOTES
The current Mars magic values and 73-byte frame header are taken from Tencent/mars:
mars/xlog/crypt/log_magic_num.h and decode_mars_nocrypt_log_file.py.
PowerShell 7 is required. Exit 0 means the bounded structure inspection completed; it does
not mean plaintext decoding or CDN qualification succeeded. Exit 1 means inspection failed.
#>
[CmdletBinding()]
param(
    [Parameter(ParameterSetName = 'Inspect')]
    [string]$LogPath,

    [Parameter(Mandatory = $true, ParameterSetName = 'SelfTest')]
    [switch]$SelfTest
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = 'Stop'

$EvidenceProtocol = 'v-local-cli/windows-chat-cdn-xlog-structure-evidence/v1'
$MaximumLogBytes = 536870912L
$CurrentMarsHeaderBytes = 73
$MagicNames = @{
    6 = 'sync_zlib_crypt'
    7 = 'async_zlib_crypt'
    8 = 'sync_zlib_no_crypt'
    9 = 'async_zlib_no_crypt'
    10 = 'sync_zstd_crypt'
    11 = 'sync_zstd_no_crypt'
    12 = 'async_zstd_crypt'
    13 = 'async_zstd_no_crypt'
}
$MagicModeOrder = @(
    'sync_zlib_crypt',
    'async_zlib_crypt',
    'sync_zlib_no_crypt',
    'async_zlib_no_crypt',
    'sync_zstd_crypt',
    'sync_zstd_no_crypt',
    'async_zstd_crypt',
    'async_zstd_no_crypt'
)
$EncryptedMagic = @(6, 7, 10, 12)

function Stop-Inspection {
    param([Parameter(Mandatory = $true)][string]$Code)
    throw [System.InvalidOperationException]::new("inspection:$Code")
}

function Assert-Inspection {
    param(
        [Parameter(Mandatory = $true)][bool]$Condition,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if (-not $Condition) {
        Stop-Inspection $Code
    }
}

function Assert-LocalAbsolutePath {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if (-not [System.IO.Path]::IsPathFullyQualified($Path) -or $Path.StartsWith('\\')) {
        Stop-Inspection $Code
    }
}

function Assert-NoReparsePoint {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    $FullPath = [System.IO.Path]::GetFullPath($Path)
    $Root = [System.IO.Path]::GetPathRoot($FullPath)
    $Current = $Root
    $Remainder = $FullPath.Substring($Root.Length)
    foreach ($Segment in $Remainder.Split([System.IO.Path]::DirectorySeparatorChar, [System.StringSplitOptions]::RemoveEmptyEntries)) {
        $Current = Join-Path $Current $Segment
        if (-not (Test-Path -LiteralPath $Current)) {
            Stop-Inspection $Code
        }
        $Item = Get-Item -LiteralPath $Current -Force -ErrorAction Stop
        if (($Item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
            Stop-Inspection $Code
        }
    }
}

function Resolve-AllowedLogPath {
    param([AllowEmptyString()][string]$ExplicitPath)

    Assert-Inspection (-not [string]::IsNullOrWhiteSpace($env:APPDATA)) 'appdata_unavailable'
    $AllowedRoot = [System.IO.Path]::GetFullPath((Join-Path $env:APPDATA 'Tencent\xwechat\log'))
    Assert-LocalAbsolutePath $AllowedRoot 'allowed_log_root_invalid'
    Assert-NoReparsePoint $AllowedRoot 'allowed_log_root_reparse_or_missing'

    $Candidate = if ([string]::IsNullOrWhiteSpace($ExplicitPath)) {
        Join-Path $AllowedRoot ('mm_' + [DateTime]::Now.ToString('yyyyMMdd') + '.xlog')
    }
    else {
        Assert-LocalAbsolutePath $ExplicitPath 'log_path_not_local_absolute'
        [System.IO.Path]::GetFullPath($ExplicitPath)
    }
    $Candidate = [System.IO.Path]::GetFullPath($Candidate)
    $AllowedPrefix = $AllowedRoot.TrimEnd([System.IO.Path]::DirectorySeparatorChar) + [System.IO.Path]::DirectorySeparatorChar
    Assert-Inspection ($Candidate.StartsWith($AllowedPrefix, [System.StringComparison]::OrdinalIgnoreCase)) 'log_path_outside_xwechat_log_root'
    Assert-Inspection ([System.IO.Path]::GetExtension($Candidate) -ceq '.xlog') 'log_extension_invalid'
    Assert-Inspection (Test-Path -LiteralPath $Candidate -PathType Leaf) 'log_file_missing'
    Assert-NoReparsePoint $Candidate 'log_path_reparse_or_missing'
    return $Candidate
}

function Read-Exactly {
    param(
        [Parameter(Mandatory = $true)][System.IO.Stream]$Stream,
        [Parameter(Mandatory = $true)][byte[]]$Buffer,
        [Parameter(Mandatory = $true)][int]$Count
    )
    $Offset = 0
    while ($Offset -lt $Count) {
        $Read = $Stream.Read($Buffer, $Offset, $Count - $Offset)
        if ($Read -le 0) {
            return $false
        }
        $Offset += $Read
    }
    return $true
}

function Inspect-XlogStructure {
    param([Parameter(Mandatory = $true)][string]$Path)

    $Before = Get-Item -LiteralPath $Path -Force -ErrorAction Stop
    Assert-Inspection (-not $Before.PSIsContainer) 'log_not_file'
    Assert-Inspection ($Before.Length -gt 0 -and $Before.Length -le $MaximumLogBytes) 'log_size_invalid'

    $Counts = [ordered]@{}
    foreach ($Name in $MagicModeOrder) {
        $Counts[$Name] = 0
    }
    $UniqueKeyFingerprints = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::Ordinal)
    $EncryptedFrames = 0
    $NoCryptFrames = 0
    $FrameCount = 0
    $Position = 0L
    $Share = [System.IO.FileShare]::ReadWrite -bor [System.IO.FileShare]::Delete
    try {
        $Stream = [System.IO.FileStream]::new(
            $Before.FullName,
            [System.IO.FileMode]::Open,
            [System.IO.FileAccess]::Read,
            $Share,
            4096,
            [System.IO.FileOptions]::SequentialScan
        )
    }
    catch [System.UnauthorizedAccessException] {
        Stop-Inspection 'log_read_access_denied'
    }
    catch [System.IO.IOException] {
        Stop-Inspection 'log_open_failed'
    }
    try {
        Assert-Inspection ($Stream.Length -eq $Before.Length) 'log_changed_before_scan'
        while ($Position -lt $Before.Length) {
            $Remaining = $Before.Length - $Position
            Assert-Inspection ($Remaining -ge ($CurrentMarsHeaderBytes + 1)) 'truncated_frame_header'
            $Header = New-Object byte[] $CurrentMarsHeaderBytes
            Assert-Inspection (Read-Exactly -Stream $Stream -Buffer $Header -Count $Header.Length) 'truncated_frame_header'

            $Magic = [int]$Header[0]
            Assert-Inspection ($MagicNames.ContainsKey($Magic)) 'unsupported_magic'
            $PayloadLength = [uint64][System.BitConverter]::ToUInt32($Header, 5)
            $FrameBytes = [uint64]$CurrentMarsHeaderBytes + $PayloadLength + 1
            Assert-Inspection ($FrameBytes -le [uint64]$Remaining) 'frame_length_out_of_bounds'

            $Name = [string]$MagicNames[$Magic]
            $Counts[$Name] = [int]$Counts[$Name] + 1
            if ($EncryptedMagic -contains $Magic) {
                $EncryptedFrames++
                $EmbeddedKey = New-Object byte[] 64
                [System.Array]::Copy($Header, 9, $EmbeddedKey, 0, 64)
                $Digest = [System.Security.Cryptography.SHA256]::HashData($EmbeddedKey)
                [void]$UniqueKeyFingerprints.Add([Convert]::ToBase64String($Digest))
                [System.Array]::Clear($EmbeddedKey, 0, $EmbeddedKey.Length)
                [System.Array]::Clear($Digest, 0, $Digest.Length)
            }
            else {
                $NoCryptFrames++
            }

            if ($PayloadLength -gt 0) {
                [void]$Stream.Seek([int64]$PayloadLength, [System.IO.SeekOrigin]::Current)
            }
            $Tail = $Stream.ReadByte()
            Assert-Inspection ($Tail -eq 0) 'frame_tail_invalid'
            [System.Array]::Clear($Header, 0, $Header.Length)
            $Position += [int64]$FrameBytes
            $FrameCount++
        }
        Assert-Inspection ($Position -eq $Before.Length -and $Stream.Position -eq $Before.Length) 'unframed_bytes_present'
    }
    finally {
        $Stream.Dispose()
    }

    $After = Get-Item -LiteralPath $Path -Force -ErrorAction Stop
    Assert-Inspection (
        $Before.Length -eq $After.Length -and
        $Before.LastWriteTimeUtc.Ticks -eq $After.LastWriteTimeUtc.Ticks
    ) 'log_changed_during_scan'
    Assert-Inspection ($FrameCount -gt 0 -and ($EncryptedFrames + $NoCryptFrames) -eq $FrameCount) 'frame_count_invalid'

    $ObservedModes = @(
        foreach ($Name in $MagicModeOrder) {
            if ([int]$Counts[$Name] -gt 0) {
                [ordered]@{ name = $Name; frames = [int]$Counts[$Name] }
            }
        }
    )
    $Status = if ($EncryptedFrames -eq $FrameCount) {
        'encrypted_mars_xlog_private_key_required'
    }
    elseif ($NoCryptFrames -eq $FrameCount) {
        'no_crypt_mars_xlog_decoder_candidate'
    }
    else {
        'mixed_mars_xlog_requires_separate_review'
    }

    return [ordered]@{
        status = $Status
        file_name = $Before.Name
        file_bytes = [int64]$Before.Length
        valid_frames = $FrameCount
        encrypted_frames = $EncryptedFrames
        no_crypt_frames = $NoCryptFrames
        unique_embedded_key_fingerprints = $UniqueKeyFingerprints.Count
        observed_modes = $ObservedModes
    }
}

function New-SyntheticFrame {
    param(
        [Parameter(Mandatory = $true)][ValidateRange(6, 13)][int]$Magic,
        [Parameter(Mandatory = $true)][byte[]]$Payload,
        [Parameter(Mandatory = $true)][byte]$KeyByte
    )
    Assert-Inspection ($MagicNames.ContainsKey($Magic)) 'self_test_magic_invalid'
    $Frame = New-Object byte[] ($CurrentMarsHeaderBytes + $Payload.Length + 1)
    $Frame[0] = [byte]$Magic
    $Frame[1] = 1
    $Frame[3] = 12
    $Frame[4] = 12
    [System.Array]::Copy([System.BitConverter]::GetBytes([uint32]$Payload.Length), 0, $Frame, 5, 4)
    for ($Index = 9; $Index -lt $CurrentMarsHeaderBytes; $Index++) {
        $Frame[$Index] = $KeyByte
    }
    [System.Array]::Copy($Payload, 0, $Frame, $CurrentMarsHeaderBytes, $Payload.Length)
    $Frame[$Frame.Length - 1] = 0
    return $Frame
}

function Join-ByteArrays {
    param([Parameter(Mandatory = $true)][object[]]$Arrays)
    $Length = 0
    foreach ($Array in $Arrays) {
        $Length += ([byte[]]$Array).Length
    }
    $Result = New-Object byte[] $Length
    $Offset = 0
    foreach ($Array in $Arrays) {
        $Bytes = [byte[]]$Array
        [System.Array]::Copy($Bytes, 0, $Result, $Offset, $Bytes.Length)
        $Offset += $Bytes.Length
    }
    return $Result
}

function Invoke-SelfTest {
    $TempBase = [System.IO.Path]::GetTempPath().TrimEnd([System.IO.Path]::DirectorySeparatorChar)
    Assert-LocalAbsolutePath $TempBase 'self_test_temp_not_local_absolute'
    $Root = Join-Path $TempBase ('v-local-xlog-structure-' + [Guid]::NewGuid().ToString('N'))
    [void](New-Item -ItemType Directory -Path $Root -ErrorAction Stop)
    try {
        $EncryptedPath = Join-Path $Root 'encrypted.xlog'
        $NoCryptPath = Join-Path $Root 'no-crypt.xlog'
        $MixedPath = Join-Path $Root 'mixed.xlog'
        $EncryptedOne = New-SyntheticFrame -Magic 7 -Payload ([byte[]](1, 2, 3)) -KeyByte 42
        $EncryptedTwo = New-SyntheticFrame -Magic 7 -Payload ([byte[]](4, 5)) -KeyByte 42
        $NoCrypt = New-SyntheticFrame -Magic 9 -Payload ([byte[]](6, 7, 8, 9)) -KeyByte 0
        [System.IO.File]::WriteAllBytes($EncryptedPath, (Join-ByteArrays -Arrays @($EncryptedOne, $EncryptedTwo)))
        [System.IO.File]::WriteAllBytes($NoCryptPath, $NoCrypt)
        [System.IO.File]::WriteAllBytes($MixedPath, (Join-ByteArrays -Arrays @($EncryptedOne, $NoCrypt)))

        $EncryptedResult = Inspect-XlogStructure -Path $EncryptedPath
        $NoCryptResult = Inspect-XlogStructure -Path $NoCryptPath
        $MixedResult = Inspect-XlogStructure -Path $MixedPath
        Assert-Inspection ($EncryptedResult.status -ceq 'encrypted_mars_xlog_private_key_required') 'self_test_encrypted_status_failed'
        Assert-Inspection ($EncryptedResult.valid_frames -eq 2 -and $EncryptedResult.unique_embedded_key_fingerprints -eq 1) 'self_test_encrypted_counts_failed'
        Assert-Inspection ($NoCryptResult.status -ceq 'no_crypt_mars_xlog_decoder_candidate') 'self_test_no_crypt_status_failed'
        Assert-Inspection ($NoCryptResult.no_crypt_frames -eq 1 -and $NoCryptResult.encrypted_frames -eq 0) 'self_test_no_crypt_counts_failed'
        Assert-Inspection ($MixedResult.status -ceq 'mixed_mars_xlog_requires_separate_review') 'self_test_mixed_status_failed'

        [ordered]@{
            protocol = $EvidenceProtocol
            status = 'self_test_passed'
            payload_decoding_performed = $false
            network_access_performed = $false
            process_memory_access_performed = $false
            embedded_key_material_output = $false
            secrets_output = $false
        } | ConvertTo-Json -Depth 4
    }
    finally {
        if (Test-Path -LiteralPath $Root) {
            $ResolvedRoot = (Resolve-Path -LiteralPath $Root).Path
            $TempPrefix = $TempBase + [System.IO.Path]::DirectorySeparatorChar
            if (-not $ResolvedRoot.StartsWith($TempPrefix, [System.StringComparison]::OrdinalIgnoreCase)) {
                Stop-Inspection 'self_test_cleanup_outside_temp'
            }
            Remove-Item -LiteralPath $ResolvedRoot -Recurse -Force
        }
    }
}

function Invoke-Inspection {
    Assert-Inspection ($PSVersionTable.PSVersion.Major -ge 7) 'powershell_7_required'
    Assert-Inspection $IsWindows 'windows_required'
    $ResolvedLogPath = Resolve-AllowedLogPath -ExplicitPath $LogPath
    $Observation = Inspect-XlogStructure -Path $ResolvedLogPath
    [ordered]@{
        protocol = $EvidenceProtocol
        status = $Observation.status
        observed_at_utc = [DateTime]::UtcNow.ToString('o')
        source = [ordered]@{
            product = 'Weixin'
            file_name = $Observation.file_name
            file_bytes = $Observation.file_bytes
            account_log_structure_read = $true
        }
        observations = [ordered]@{
            valid_frames = $Observation.valid_frames
            encrypted_frames = $Observation.encrypted_frames
            no_crypt_frames = $Observation.no_crypt_frames
            unique_embedded_key_fingerprints = $Observation.unique_embedded_key_fingerprints
            observed_modes = $Observation.observed_modes
        }
        conclusions = [ordered]@{
            current_mars_frame_structure = 'observed'
            no_crypt_decoder_applicable = ($Observation.encrypted_frames -eq 0)
            encrypted_decoder_requires_matching_private_key = ($Observation.encrypted_frames -gt 0)
            matching_private_key_available = 'not_evaluated'
            payload_decoding_performed = $false
            plaintext_event_binding = 'not_observed'
            descriptor_to_runtime_request_binding = 'not_observed'
            endpoint_qualification = 'not_qualified'
            network_access_performed = $false
            process_memory_access_performed = $false
            log_plaintext_output = $false
            embedded_key_material_output = $false
            secrets_output = $false
        }
        limitations = @(
            'frame magic identifies the Mars storage mode but does not identify a chat CDN event',
            'encrypted payloads require the matching application private key or a vendor-matched decoder',
            'this inspection does not decode, decompress, or search log payloads',
            'successful inspection does not qualify a CDN endpoint, request, response, or descriptor lifetime'
        )
    } | ConvertTo-Json -Depth 8
}

try {
    if ($SelfTest) {
        Invoke-SelfTest
    }
    else {
        Invoke-Inspection
    }
}
catch {
    $Code = 'inspection_failed'
    if ($_.Exception.Message -cmatch '^inspection:([a-z0-9_]+)$') {
        $Code = $Matches[1]
    }
    [ordered]@{
        protocol = $EvidenceProtocol
        status = 'failed'
        error_code = $Code
        payload_decoding_performed = $false
        network_access_performed = $false
        process_memory_access_performed = $false
        embedded_key_material_output = $false
        secrets_output = $false
    } | ConvertTo-Json -Depth 4
    exit 1
}
