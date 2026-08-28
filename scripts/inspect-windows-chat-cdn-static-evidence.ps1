<#
.SYNOPSIS
Collects secret-free static chat-CDN evidence from the installed Windows Weixin client.

.DESCRIPTION
Reads selected installed-distribution binaries as ordinary files, binds the observation to
their version, size, and SHA-256, and checks a fixed set of ASCII implementation markers.
It never reads process memory, account files, messages, URLs from a database, or network
traffic. Static evidence cannot qualify a runtime protocol or authorize CDN access.

.NOTES
Exit 0 means the complete contemporary static marker set was observed, 1 means the
inspection failed safely, and 2 means the client was found but the marker set was partial.
PowerShell 7 is required.
#>
[CmdletBinding()]
param(
    [string]$InstallDirectory,
    [switch]$ShowPaths,
    [switch]$SelfTest
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = 'Stop'

$EvidenceProtocol = 'v-local-cli/windows-chat-cdn-static-evidence/v1'
$MaximumBinaryBytes = 1073741824L
$DefaultChunkBytes = 4194304

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

function Resolve-WeixinVersionDirectory {
    param([AllowEmptyString()][string]$ExplicitDirectory)

    $Candidates = [System.Collections.Generic.List[string]]::new()
    if (-not [string]::IsNullOrWhiteSpace($ExplicitDirectory)) {
        Assert-LocalAbsolutePath $ExplicitDirectory 'install_directory_not_local_absolute'
        $ExplicitFullPath = [System.IO.Path]::GetFullPath($ExplicitDirectory)
        Assert-NoReparsePoint $ExplicitFullPath 'install_directory_reparse_or_missing'
        if (Test-Path -LiteralPath (Join-Path $ExplicitFullPath 'Weixin.dll') -PathType Leaf) {
            $Candidates.Add($ExplicitFullPath)
        }
        elseif (Test-Path -LiteralPath (Join-Path $ExplicitFullPath 'Weixin.exe') -PathType Leaf) {
            $Launcher = Get-Item -LiteralPath (Join-Path $ExplicitFullPath 'Weixin.exe') -Force
            $Version = [string]$Launcher.VersionInfo.FileVersion
            if (-not [string]::IsNullOrWhiteSpace($Version)) {
                $Candidates.Add((Join-Path $ExplicitFullPath $Version))
            }
        }
    }
    else {
        $LauncherPaths = @(
            Get-Process -Name Weixin -ErrorAction SilentlyContinue |
                ForEach-Object {
                    try { $_.Path } catch { $null }
                } |
                Where-Object { -not [string]::IsNullOrWhiteSpace($_) } |
                Sort-Object -Unique
        )
        foreach ($LauncherPath in $LauncherPaths) {
            Assert-LocalAbsolutePath $LauncherPath 'running_client_path_not_local_absolute'
            $Launcher = Get-Item -LiteralPath $LauncherPath -Force
            $Version = [string]$Launcher.VersionInfo.FileVersion
            if (-not [string]::IsNullOrWhiteSpace($Version)) {
                $Candidates.Add((Join-Path $Launcher.DirectoryName $Version))
            }
        }
        foreach ($KnownRoot in @(
            'C:\Program Files\Tencent\Weixin',
            'C:\Program Files (x86)\Tencent\Weixin'
        )) {
            $LauncherPath = Join-Path $KnownRoot 'Weixin.exe'
            if (Test-Path -LiteralPath $LauncherPath -PathType Leaf) {
                $Launcher = Get-Item -LiteralPath $LauncherPath -Force
                $Version = [string]$Launcher.VersionInfo.FileVersion
                if (-not [string]::IsNullOrWhiteSpace($Version)) {
                    $Candidates.Add((Join-Path $KnownRoot $Version))
                }
            }
        }
    }

    $ValidCandidates = @(
        $Candidates |
            ForEach-Object { [System.IO.Path]::GetFullPath($_) } |
            Sort-Object -Unique |
            Where-Object {
                (Test-Path -LiteralPath (Join-Path $_ 'Weixin.dll') -PathType Leaf) -and
                (Test-Path -LiteralPath (Join-Path $_ 'ilink2.dll') -PathType Leaf) -and
                (Test-Path -LiteralPath (Join-Path $_ 'ilink_wrapper.dll') -PathType Leaf)
            }
    )
    Assert-Inspection ($ValidCandidates.Count -gt 0) 'current_weixin_installation_not_found'
    Assert-Inspection ($ValidCandidates.Count -eq 1) 'current_weixin_installation_ambiguous'
    Assert-NoReparsePoint $ValidCandidates[0] 'weixin_version_directory_reparse_or_missing'
    return $ValidCandidates[0]
}

function Read-BinaryStaticEvidence {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][System.Collections.IDictionary]$Markers,
        [int]$ChunkBytes = $DefaultChunkBytes
    )
    Assert-LocalAbsolutePath $Path 'binary_path_not_local_absolute'
    Assert-NoReparsePoint $Path 'binary_reparse_or_missing'
    Assert-Inspection ($ChunkBytes -ge 32 -and $ChunkBytes -le 16777216) 'chunk_size_invalid'

    $Before = Get-Item -LiteralPath $Path -Force
    Assert-Inspection (-not $Before.PSIsContainer) 'binary_not_file'
    Assert-Inspection ($Before.Length -gt 0 -and $Before.Length -le $MaximumBinaryBytes) 'binary_size_invalid'

    $MarkerStatus = [ordered]@{}
    $MaximumMarkerLength = 1
    foreach ($Name in $Markers.Keys) {
        $Marker = [string]$Markers[$Name]
        Assert-Inspection (-not [string]::IsNullOrWhiteSpace($Marker)) 'empty_static_marker'
        $MarkerStatus[$Name] = $false
        if ($Marker.Length -gt $MaximumMarkerLength) {
            $MaximumMarkerLength = $Marker.Length
        }
    }

    $Hasher = [System.Security.Cryptography.IncrementalHash]::CreateHash(
        [System.Security.Cryptography.HashAlgorithmName]::SHA256
    )
    $Share = [System.IO.FileShare]::ReadWrite -bor [System.IO.FileShare]::Delete
    $Stream = [System.IO.FileStream]::new(
        $Before.FullName,
        [System.IO.FileMode]::Open,
        [System.IO.FileAccess]::Read,
        $Share,
        $ChunkBytes,
        [System.IO.FileOptions]::SequentialScan
    )
    $Buffer = New-Object byte[] $ChunkBytes
    $Tail = ''
    try {
        while (($Read = $Stream.Read($Buffer, 0, $Buffer.Length)) -gt 0) {
            $Hasher.AppendData($Buffer, 0, $Read)
            $Text = $Tail + [System.Text.Encoding]::ASCII.GetString($Buffer, 0, $Read)
            foreach ($Name in $Markers.Keys) {
                if (-not $MarkerStatus[$Name] -and
                    $Text.IndexOf([string]$Markers[$Name], [System.StringComparison]::Ordinal) -ge 0) {
                    $MarkerStatus[$Name] = $true
                }
            }
            $Overlap = [Math]::Min($MaximumMarkerLength - 1, $Text.Length)
            $Tail = if ($Overlap -gt 0) { $Text.Substring($Text.Length - $Overlap) } else { '' }
        }
        $HashBytes = $Hasher.GetHashAndReset()
    }
    finally {
        $Stream.Dispose()
        $Hasher.Dispose()
    }

    $After = Get-Item -LiteralPath $Path -Force
    Assert-Inspection (
        $Before.Length -eq $After.Length -and
        $Before.LastWriteTimeUtc.Ticks -eq $After.LastWriteTimeUtc.Ticks
    ) 'binary_changed_during_scan'

    return [pscustomobject]@{
        Name = $Before.Name
        Version = [string]$Before.VersionInfo.FileVersion
        Length = [int64]$Before.Length
        Sha256 = [Convert]::ToHexString($HashBytes).ToLowerInvariant()
        Markers = $MarkerStatus
    }
}

function Invoke-SelfTest {
    $TempBase = [System.IO.Path]::GetTempPath().TrimEnd([System.IO.Path]::DirectorySeparatorChar)
    Assert-LocalAbsolutePath $TempBase 'self_test_temp_not_local_absolute'
    $Root = Join-Path $TempBase ('v-local-chat-cdn-static-' + [Guid]::NewGuid().ToString('N'))
    [void](New-Item -ItemType Directory -Path $Root -ErrorAction Stop)
    try {
        $Fixture = Join-Path $Root 'fixture.bin'
        $CrossBoundaryMarker = 'marker-crosses-the-chunk-boundary'
        $Payload = ('x' * 29) + $CrossBoundaryMarker + ('y' * 80)
        [System.IO.File]::WriteAllBytes($Fixture, [System.Text.Encoding]::ASCII.GetBytes($Payload))
        $Markers = [ordered]@{
            cross_boundary = $CrossBoundaryMarker
            absent = 'marker-that-is-not-present'
        }
        $Result = Read-BinaryStaticEvidence -Path $Fixture -Markers $Markers -ChunkBytes 32
        Assert-Inspection ($Result.Markers['cross_boundary'] -eq $true) 'self_test_cross_boundary_missed'
        Assert-Inspection ($Result.Markers['absent'] -eq $false) 'self_test_false_positive'
        Assert-Inspection ($Result.Sha256 -cmatch '^[0-9a-f]{64}$') 'self_test_hash_invalid'
        [ordered]@{
            protocol = $EvidenceProtocol
            status = 'self_test_passed'
            network_access_performed = $false
            process_memory_access_performed = $false
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

    $VersionDirectory = Resolve-WeixinVersionDirectory -ExplicitDirectory $InstallDirectory
    $WeixinMarkers = [ordered]@{
        descriptor_high = 'cdnbigimgurl'
        descriptor_medium = 'cdnmidimgurl'
        descriptor_thumbnail = 'cdnthumburl'
        c2c_image_task = 'TaskFactory::CreateC2CImageDownloadTask'
        c2c_download_api = 'mars::cdn::StartC2CDownload'
        dynamic_route_request = 'cgi-bin/micromsg-bin/getcdndns'
        dynamic_route_apply = 'setcdndns: uin %_, rule.len %_, saferule.len %_'
        rsa_parameter_api = 'mars::cdn::CdnCore::set_rsa_params'
        rsa_parameter_gate = 'c2ctask no rsa params present. need set rsa params first.'
        download_binding_fields = 'download param: fullpath:%_,aeskey:%_,fileId:%_,clientid:%_,filelen:%_'
        https_download_alternative = 'TaskFactory::CreateHttpsDownloadTask'
        novac2c_host = 'novac2c.cdn.weixin.qq.com'
        encrypted_query_param = 'encrypted_query_param'
        c2c_download_http_path = '/c2c/download'
    }
    $IlinkMarkers = [ordered]@{
        c2c_download_async = 'C2CDownloadAsync@NetworkManagerNoPB'
        network_manager_factory = 'CreateNetworkManagerNoPB'
        auth_session_accessor = 'GetAuthRawData@AuthManagerNoPB'
        real_uin_accessor = 'RealUin@NetworkManagerNoPB'
    }
    $IlinkCoreMarkers = [ordered]@{
        c2c_download_core = 'mars::cdn::CdnCore::start_c2c_download'
        rsa_parameter_core = 'mars::cdn::CdnCore::set_rsa_params'
        ilink_c2c_message = 'IlinkC2CDownload'
    }

    $Weixin = Read-BinaryStaticEvidence -Path (Join-Path $VersionDirectory 'Weixin.dll') -Markers $WeixinMarkers
    $IlinkWrapper = Read-BinaryStaticEvidence -Path (Join-Path $VersionDirectory 'ilink_wrapper.dll') -Markers $IlinkMarkers
    $IlinkCore = Read-BinaryStaticEvidence -Path (Join-Path $VersionDirectory 'ilink2.dll') -Markers $IlinkCoreMarkers

    $Required = @(
        $Weixin.Markers['descriptor_high'],
        $Weixin.Markers['descriptor_medium'],
        $Weixin.Markers['descriptor_thumbnail'],
        $Weixin.Markers['c2c_image_task'],
        $Weixin.Markers['c2c_download_api'],
        $Weixin.Markers['dynamic_route_request'],
        $Weixin.Markers['dynamic_route_apply'],
        $Weixin.Markers['rsa_parameter_api'],
        $Weixin.Markers['rsa_parameter_gate'],
        $Weixin.Markers['download_binding_fields'],
        $IlinkWrapper.Markers['c2c_download_async'],
        $IlinkWrapper.Markers['network_manager_factory'],
        $IlinkCore.Markers['c2c_download_core'],
        $IlinkCore.Markers['rsa_parameter_core']
    )
    $Complete = @($Required | Where-Object { $_ -ne $true }).Count -eq 0
    $Status = if ($Complete) {
        'current_client_static_stack_present_unbound'
    } else {
        'partial_current_client_static_evidence'
    }

    $Files = @(
        foreach ($Scan in @($Weixin, $IlinkWrapper, $IlinkCore)) {
            [ordered]@{
                name = $Scan.Name
                version = $Scan.Version
                length = $Scan.Length
                sha256 = $Scan.Sha256
            }
        }
    )
    $ClientVersion = @($Files | ForEach-Object { $_.version } | Where-Object { -not [string]::IsNullOrWhiteSpace($_) } | Select-Object -First 1)
    $Report = [ordered]@{
        protocol = $EvidenceProtocol
        status = $Status
        observed_at_utc = [DateTime]::UtcNow.ToString('o')
        client = [ordered]@{
            product = 'Weixin'
            version = if ($ClientVersion.Count -gt 0) { $ClientVersion[0] } else { Split-Path -Leaf $VersionDirectory }
            files = $Files
        }
        observations = [ordered]@{
            message_descriptor_fields_in_weixin_binary = (
                $Weixin.Markers['descriptor_high'] -and
                $Weixin.Markers['descriptor_medium'] -and
                $Weixin.Markers['descriptor_thumbnail']
            )
            c2c_image_task_in_weixin_binary = $Weixin.Markers['c2c_image_task']
            c2c_download_stack_in_weixin_binary = $Weixin.Markers['c2c_download_api']
            dynamic_cdn_route_in_weixin_binary = (
                $Weixin.Markers['dynamic_route_request'] -and $Weixin.Markers['dynamic_route_apply']
            )
            rsa_parameter_gate_in_weixin_binary = (
                $Weixin.Markers['rsa_parameter_api'] -and $Weixin.Markers['rsa_parameter_gate']
            )
            download_binding_fields_in_weixin_binary = $Weixin.Markers['download_binding_fields']
            https_download_alternative_in_weixin_binary = $Weixin.Markers['https_download_alternative']
            novac2c_host_in_weixin_binary = $Weixin.Markers['novac2c_host']
            encrypted_query_param_static_marker = $Weixin.Markers['encrypted_query_param']
            c2c_download_http_path_static_marker = $Weixin.Markers['c2c_download_http_path']
            ilink_c2c_api_in_wrapper_binary = (
                $IlinkWrapper.Markers['c2c_download_async'] -and
                $IlinkWrapper.Markers['network_manager_factory']
            )
            ilink_session_accessors_in_wrapper_binary = (
                $IlinkWrapper.Markers['auth_session_accessor'] -and
                $IlinkWrapper.Markers['real_uin_accessor']
            )
            c2c_and_rsa_core_in_ilink_binary = (
                $IlinkCore.Markers['c2c_download_core'] -and
                $IlinkCore.Markers['rsa_parameter_core'] -and
                $IlinkCore.Markers['ilink_c2c_message']
            )
            descriptor_and_c2c_markers_co_located_in_same_binary = (
                $Weixin.Markers['descriptor_high'] -and $Weixin.Markers['c2c_image_task']
            )
        }
        conclusions = [ordered]@{
            contemporary_client_static_evidence = $Complete
            descriptor_to_runtime_request_binding = 'not_observed'
            runtime_protocol_selection = 'not_observed'
            endpoint_qualification = 'not_qualified'
            response_crypto_qualification = 'not_qualified'
            minimum_session_material = 'unknown'
            descriptor_freshness = 'unknown_without_verified_request'
            remote_acquisition_implemented = $false
            network_access_performed = $false
            process_memory_access_performed = $false
            account_data_access_performed = $false
            secrets_output = $false
        }
        limitations = @(
            'static marker presence can include dormant or unrelated code paths',
            'static marker absence does not prove a runtime feature is absent',
            'co-location in one binary does not bind a message descriptor to a request path',
            'this report does not authorize or perform a real CDN request'
        )
    }
    if ($ShowPaths) {
        $Report.client['install_directory'] = $VersionDirectory
    }
    $Report | ConvertTo-Json -Depth 8
    if (-not $Complete) {
        exit 2
    }
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
        network_access_performed = $false
        process_memory_access_performed = $false
        secrets_output = $false
    } | ConvertTo-Json -Depth 4
    exit 1
}
