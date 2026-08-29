<#
.SYNOPSIS
Runs the W64-08 four-fixture chat-image recovery acceptance gate on Windows amd64.

.DESCRIPTION
Probes four distinct image evidence IDs, requests an exact user confirmation only for
the two cases with a decodable lower cache tier but no higher local tier, then performs one
saved-key refresh and one retry per confirmed evidence. A local WXGF candidate may be an
expected decoder-unavailable error and remains bound to the same snapshot generation.
This local/manual W64-08 gate never enables Chat CDN access. The separate
recover-chat-image consent gate covers the bounded network path. Private image artifacts
and the sanitized report are stored in separate current-user directories.

.EXAMPLE
Get-Help .\scripts\accept-windows-chat-image-recovery.ps1 -Detailed

.NOTES
Exit 0 means pass, 1 means fail, and 2 means inconclusive. PowerShell 7 is required.
#>
[CmdletBinding()]
param(
    [string]$Cli,
    [string]$Account,
    [string]$LowerTierMissingEvidenceId,
    [string]$DecodableHighEvidenceId,
    [string]$WxgfCandidateEvidenceId,
    [string]$ExpiryUnknownDescriptorEvidenceId,
    [string]$PrivateRootBase,
    [string]$EvidenceRootBase,
    [ValidateSet('Prompt', 'Skip')]
    [string]$RecoveryMode = 'Prompt',
    [switch]$ShowPaths,
    [switch]$SelfTest
)

Set-StrictMode -Version 3.0

function Stop-Acceptance {
    param([Parameter(Mandatory = $true)][string]$Code)
    throw [System.InvalidOperationException]::new("acceptance:$Code")
}

function Assert-Acceptance {
    param(
        [Parameter(Mandatory = $true)][bool]$Condition,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if (-not $Condition) {
        Stop-Acceptance $Code
    }
}

function Get-Field {
    param(
        [AllowNull()][object]$Object,
        [Parameter(Mandatory = $true)][string]$Name
    )
    if ($null -eq $Object) {
        return $null
    }
    $Property = $Object.PSObject.Properties[$Name]
    if ($null -eq $Property) {
        return $null
    }
    return $Property.Value
}

function Assert-PrivateIdentifier {
    param(
        [Parameter(Mandatory = $true)][string]$Value,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if ([string]::IsNullOrWhiteSpace($Value) -or $Value.Length -gt 4096) {
        Stop-Acceptance $Code
    }
    foreach ($Character in $Value.ToCharArray()) {
        if ([char]::IsControl($Character)) {
            Stop-Acceptance $Code
        }
    }
}

function Assert-LocalAbsolutePath {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if (-not [System.IO.Path]::IsPathFullyQualified($Path) -or $Path.StartsWith('\\')) {
        Stop-Acceptance $Code
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
            continue
        }
        $Item = Get-Item -LiteralPath $Current -Force -ErrorAction Stop
        if (($Item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
            Stop-Acceptance $Code
        }
    }
}

function Set-PrivateDirectoryAcl {
    param([Parameter(Mandatory = $true)][string]$Path)
    $CurrentUser = [System.Security.Principal.WindowsIdentity]::GetCurrent().User
    $System = [System.Security.Principal.SecurityIdentifier]::new('S-1-5-18')
    $Administrators = [System.Security.Principal.SecurityIdentifier]::new('S-1-5-32-544')
    $Inheritance = [System.Security.AccessControl.InheritanceFlags]::ContainerInherit -bor
        [System.Security.AccessControl.InheritanceFlags]::ObjectInherit
    $Propagation = [System.Security.AccessControl.PropagationFlags]::None
    $Allow = [System.Security.AccessControl.AccessControlType]::Allow
    $Security = [System.Security.AccessControl.DirectorySecurity]::new()
    $Security.SetAccessRuleProtection($true, $false)
    $Security.SetOwner($CurrentUser)
    foreach ($Identity in @($CurrentUser, $System, $Administrators)) {
        $Rule = [System.Security.AccessControl.FileSystemAccessRule]::new(
            $Identity,
            [System.Security.AccessControl.FileSystemRights]::FullControl,
            $Inheritance,
            $Propagation,
            $Allow
        )
        [void]$Security.AddAccessRule($Rule)
    }
    Set-Acl -LiteralPath $Path -AclObject $Security -ErrorAction Stop
}

function New-PrivateRunDirectory {
    param(
        [Parameter(Mandatory = $true)][string]$Base,
        [Parameter(Mandatory = $true)][string]$RunId,
        [Parameter(Mandatory = $true)][string]$Code
    )
    Assert-LocalAbsolutePath $Base $Code
    if (-not (Test-Path -LiteralPath $Base)) {
        [void](New-Item -ItemType Directory -Path $Base -ErrorAction Stop)
    }
    Assert-NoReparsePoint $Base $Code
    $RunDirectory = Join-Path $Base $RunId
    if (Test-Path -LiteralPath $RunDirectory) {
        Stop-Acceptance $Code
    }
    [void](New-Item -ItemType Directory -Path $RunDirectory -ErrorAction Stop)
    Assert-NoReparsePoint $RunDirectory $Code
    Set-PrivateDirectoryAcl $RunDirectory
    return [System.IO.Path]::GetFullPath($RunDirectory)
}

function Invoke-CliJson {
    param(
        [Parameter(Mandatory = $true)][string]$Executable,
        [Parameter(Mandatory = $true)][string[]]$Arguments
    )
    try {
        $NativeOutput = @(& $Executable @Arguments 2>&1)
        $ExitCode = $LASTEXITCODE
    }
    catch {
        Stop-Acceptance 'cli_process_start_failed'
    }
    $Text = ($NativeOutput | ForEach-Object { $_.ToString() }) -join [Environment]::NewLine
    if ([string]::IsNullOrWhiteSpace($Text) -or $Text.Length -gt 1048576) {
        Stop-Acceptance 'cli_json_size_invalid'
    }
    try {
        $Envelope = $Text | ConvertFrom-Json -ErrorAction Stop
    }
    catch {
        Stop-Acceptance 'cli_json_invalid'
    }
    return [pscustomobject]@{
        ExitCode = $ExitCode
        Envelope = $Envelope
    }
}

function Assert-SucceededEnvelope {
    param(
        [Parameter(Mandatory = $true)][object]$Invocation,
        [Parameter(Mandatory = $true)][string]$Code
    )
    $Envelope = Get-Field $Invocation 'Envelope'
    Assert-Acceptance (($Invocation.ExitCode -eq 0) -and ((Get-Field $Envelope 'schema_version') -eq 1) -and
        ((Get-Field $Envelope 'command_status') -ceq 'succeeded')) $Code
    Assert-Acceptance ($null -ne (Get-Field $Envelope 'data')) $Code
    return $Envelope
}

function Assert-KnownProbeEnums {
    param([Parameter(Mandatory = $true)][object]$Data)
    $Quality = [string](Get-Field $Data 'quality_tier')
    $HigherStatus = [string](Get-Field $Data 'higher_quality_local_status')
    $RecoveryAction = [string](Get-Field $Data 'higher_quality_recovery_action')
    $RemoteStatus = [string](Get-Field $Data 'remote_descriptor_status')
    $RemoteParseStatus = [string](Get-Field $Data 'remote_descriptor_parse_status')
    $ProtocolStatus = [string](Get-Field $Data 'remote_protocol_status')
    $AcquisitionStatus = [string](Get-Field $Data 'remote_acquisition_status')
    $Format = [string](Get-Field $Data 'format')
    $QualityBasis = [string](Get-Field $Data 'quality_basis')
    $QualityClaimScope = [string](Get-Field $Data 'quality_claim_scope')
    $SourceOriginalQualityStatus = [string](Get-Field $Data 'source_original_quality_status')
    $DimensionsRole = [string](Get-Field $Data 'dimensions_role')
    Assert-Acceptance (@('high', 'medium', 'thumbnail', 'unknown') -ccontains $Quality) 'probe_quality_enum_invalid'
    Assert-Acceptance (@('not_applicable', 'missing', 'decoder_unavailable', 'validation_failed', 'unknown') -ccontains $HigherStatus) 'probe_higher_status_enum_invalid'
    Assert-Acceptance (@('none', 'run_recover_chat_image_offline_then_request_structured_consent', 'ask_user_to_open_original_then_refresh_and_retry', 'do_not_request_redownload_same_candidate', 'inspect_key_or_format_before_retry', 'manual_review') -ccontains $RecoveryAction) 'probe_recovery_action_enum_invalid'
    Assert-Acceptance (@('present_expiry_unknown', 'missing', 'unknown') -ccontains $RemoteStatus) 'probe_remote_status_enum_invalid'
    Assert-Acceptance (@('parsed_unverified_protocol', 'parsed_partial_unverified_protocol', 'present_incomplete', 'present_invalid', 'not_applicable', 'not_evaluated') -ccontains $RemoteParseStatus) 'probe_remote_parse_status_enum_invalid'
    Assert-Acceptance (@('direct_https_descriptor_response_unverified', 'unverified_desktop_protocol', 'not_applicable', 'not_evaluated') -ccontains $ProtocolStatus) 'probe_protocol_status_enum_invalid'
    Assert-Acceptance (@('inspect_via_recover_chat_image_with_single_attempt_consent', 'unavailable_unverified_protocol', 'not_available_no_descriptor', 'not_evaluated') -ccontains $AcquisitionStatus) 'probe_acquisition_status_enum_invalid'
    Assert-Acceptance (@('jpg', 'png', 'gif') -ccontains $Format) 'probe_format_enum_invalid'
    Assert-Acceptance ($QualityBasis -ceq 'hardlink_cache_filename_variant') 'probe_quality_basis_invalid'
    Assert-Acceptance ($QualityClaimScope -ceq 'wechat_cache_variant_only') 'probe_quality_claim_scope_invalid'
    Assert-Acceptance ($SourceOriginalQualityStatus -ceq 'unknown') 'probe_source_original_quality_overclaim'
    Assert-Acceptance ((Get-Field $Data 'source_original_dimensions_known') -eq $false) 'probe_original_dimensions_claim_invalid'
    Assert-Acceptance ($DimensionsRole -ceq 'decoded_output_observation_not_quality_gate') 'probe_dimensions_role_invalid'
    Assert-Acceptance (([int](Get-Field $Data 'width') -gt 0) -and ([int](Get-Field $Data 'height') -gt 0) -and
        ([int64](Get-Field $Data 'bytes') -gt 0)) 'probe_dimensions_or_size_invalid'
    switch ($HigherStatus) {
        'not_applicable' { Assert-Acceptance (($Quality -ceq 'high') -and ($RecoveryAction -ceq 'none')) 'probe_quality_state_inconsistent' }
        'missing' { Assert-Acceptance ($RecoveryAction -ceq 'ask_user_to_open_original_then_refresh_and_retry') 'probe_quality_state_inconsistent' }
        'decoder_unavailable' {
            Assert-Acceptance (($RecoveryAction -ceq 'do_not_request_redownload_same_candidate') -and
                (@('wxgf', 'webp') -ccontains [string](Get-Field $Data 'higher_quality_detected_format'))) 'probe_quality_state_inconsistent'
        }
        'validation_failed' { Assert-Acceptance ($RecoveryAction -ceq 'inspect_key_or_format_before_retry') 'probe_quality_state_inconsistent' }
        'unknown' { Assert-Acceptance ($RecoveryAction -ceq 'manual_review') 'probe_quality_state_inconsistent' }
    }
    switch ($RemoteStatus) {
        'present_expiry_unknown' {
            Assert-Acceptance ((@('parsed_unverified_protocol', 'parsed_partial_unverified_protocol', 'present_incomplete', 'present_invalid') -ccontains $RemoteParseStatus) -and
                ($ProtocolStatus -ceq 'unverified_desktop_protocol') -and
                ($AcquisitionStatus -ceq 'unavailable_unverified_protocol')) 'probe_remote_state_inconsistent'
        }
        'missing' {
            Assert-Acceptance (($RemoteParseStatus -ceq 'not_applicable') -and ($ProtocolStatus -ceq 'not_applicable') -and
                ($AcquisitionStatus -ceq 'not_available_no_descriptor')) 'probe_remote_state_inconsistent'
        }
        'unknown' {
            Assert-Acceptance (($RemoteParseStatus -ceq 'not_evaluated') -and ($ProtocolStatus -ceq 'not_evaluated') -and
                ($AcquisitionStatus -ceq 'not_evaluated')) 'probe_remote_state_inconsistent'
        }
    }
}

function Assert-FixtureState {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][object]$Data
    )
    $Quality = [string](Get-Field $Data 'quality_tier')
    $HigherStatus = [string](Get-Field $Data 'higher_quality_local_status')
    $RecoveryAction = [string](Get-Field $Data 'higher_quality_recovery_action')
    switch ($Name) {
        'lower_tier_missing' {
            Assert-Acceptance ((@('medium', 'thumbnail') -ccontains $Quality) -and ($HigherStatus -ceq 'missing') -and
                ($RecoveryAction -ceq 'ask_user_to_open_original_then_refresh_and_retry')) 'lower_tier_missing_contract_failed'
        }
        'decodable_high' {
            Assert-Acceptance (($Quality -ceq 'high') -and ($HigherStatus -ceq 'not_applicable') -and
                ($RecoveryAction -ceq 'none')) 'decodable_high_contract_failed'
        }
        'wxgf_candidate' {
            Assert-Acceptance ((@('medium', 'thumbnail') -ccontains $Quality) -and ($HigherStatus -ceq 'decoder_unavailable') -and
                ([string](Get-Field $Data 'higher_quality_detected_format') -ceq 'wxgf') -and
                ($RecoveryAction -ceq 'do_not_request_redownload_same_candidate')) 'wxgf_candidate_contract_failed'
        }
        'expiry_unknown_descriptor' {
            Assert-Acceptance ((@('medium', 'thumbnail') -ccontains $Quality) -and ($HigherStatus -ceq 'missing') -and
                ($RecoveryAction -ceq 'ask_user_to_open_original_then_refresh_and_retry') -and
                ([string](Get-Field $Data 'remote_descriptor_status') -ceq 'present_expiry_unknown') -and
                ([string](Get-Field $Data 'remote_descriptor_parse_status') -ceq 'parsed_unverified_protocol') -and
                ([string](Get-Field $Data 'remote_protocol_status') -ceq 'unverified_desktop_protocol') -and
                ([string](Get-Field $Data 'remote_acquisition_status') -ceq 'unavailable_unverified_protocol')) 'expiry_unknown_descriptor_contract_failed'
        }
        default {
            Stop-Acceptance 'fixture_name_invalid'
        }
    }
}

function Assert-DecoderUnavailableProbe {
    param(
        [Parameter(Mandatory = $true)][object]$Invocation,
        [Parameter(Mandatory = $true)][string]$OutputPath
    )
    $Envelope = Get-Field $Invocation 'Envelope'
    $ErrorValue = Get-Field $Envelope 'error'
    $Details = Get-Field $ErrorValue 'details'
    $Meta = Get-Field $Envelope 'meta'
    Assert-Acceptance (($Invocation.ExitCode -eq 5) -and ((Get-Field $Envelope 'schema_version') -eq 1) -and
        ((Get-Field $Envelope 'command_status') -ceq 'failed') -and
        ([string](Get-Field $ErrorValue 'type') -ceq 'chat_image_unavailable') -and
        ($null -eq (Get-Field $Envelope 'data'))) 'wxgf_error_envelope_invalid'
    $Quality = [string](Get-Field $Details 'quality_tier')
    $RemoteStatus = [string](Get-Field $Details 'remote_descriptor_status')
    $RemoteParseStatus = [string](Get-Field $Details 'remote_descriptor_parse_status')
    $ProtocolStatus = [string](Get-Field $Details 'remote_protocol_status')
    $AcquisitionStatus = [string](Get-Field $Details 'remote_acquisition_status')
    Assert-Acceptance ((@('high', 'medium', 'thumbnail') -ccontains $Quality) -and
        ([string](Get-Field $Details 'local_resolution_status') -ceq 'decoder_unavailable') -and
        ([string](Get-Field $Details 'detected_format') -ceq 'wxgf') -and
        ([string](Get-Field $Details 'recovery_action') -ceq 'do_not_request_redownload_same_candidate')) 'wxgf_error_contract_failed'
    Assert-Acceptance (([string](Get-Field $Details 'quality_basis') -ceq 'hardlink_cache_filename_variant') -and
        ([string](Get-Field $Details 'quality_claim_scope') -ceq 'wechat_cache_variant_only') -and
        ((Get-Field $Details 'source_original_dimensions_known') -eq $false) -and
        ([string](Get-Field $Details 'source_original_quality_status') -ceq 'unknown')) 'wxgf_error_quality_claim_failed'
    Assert-Acceptance ((Get-Field $Details 'network_access_performed') -eq $false) 'chat_image_network_boundary_failed'
    Assert-Acceptance (@('present_expiry_unknown', 'missing', 'unknown') -ccontains $RemoteStatus) 'wxgf_remote_status_invalid'
    switch ($RemoteStatus) {
        'present_expiry_unknown' {
            Assert-Acceptance ((@('parsed_unverified_protocol', 'parsed_partial_unverified_protocol', 'present_incomplete', 'present_invalid') -ccontains $RemoteParseStatus) -and
                ($ProtocolStatus -ceq 'unverified_desktop_protocol') -and
                ($AcquisitionStatus -ceq 'unavailable_unverified_protocol')) 'wxgf_remote_state_inconsistent'
        }
        'missing' {
            Assert-Acceptance (($RemoteParseStatus -ceq 'not_applicable') -and ($ProtocolStatus -ceq 'not_applicable') -and
                ($AcquisitionStatus -ceq 'not_available_no_descriptor')) 'wxgf_remote_state_inconsistent'
        }
        'unknown' {
            Assert-Acceptance (($RemoteParseStatus -ceq 'not_evaluated') -and ($ProtocolStatus -ceq 'not_evaluated') -and
                ($AcquisitionStatus -ceq 'not_evaluated')) 'wxgf_remote_state_inconsistent'
        }
    }
    Assert-Acceptance (-not [string]::IsNullOrWhiteSpace([string](Get-Field $Meta 'generation_id'))) 'image_generation_missing'
    Assert-Acceptance (-not [string]::IsNullOrWhiteSpace([string](Get-Field $Meta 'snapshot_manifest_sha256'))) 'image_manifest_missing'
    Assert-Acceptance (-not (Test-Path -LiteralPath $OutputPath)) 'wxgf_error_created_output'
    return [pscustomobject]@{
        Envelope = $Envelope
        Data = $Details
        LocalResolutionStatus = 'decoder_unavailable'
        Generation = [string](Get-Field $Meta 'generation_id')
        Manifest = [string](Get-Field $Meta 'snapshot_manifest_sha256')
        Public = [ordered]@{
            fixture = ''
            status = 'pass_expected_decoder_unavailable'
            quality_tier = $Quality
            quality_claim_scope = [string](Get-Field $Details 'quality_claim_scope')
            source_original_dimensions_known = $false
            source_original_quality_status = 'unknown'
            dimensions_role = 'no_decoded_output'
            dimensions_are_observational_not_quality_gate = $true
            width = $null
            height = $null
            bytes = $null
            local_resolution_status = 'decoder_unavailable'
            higher_quality_local_status = $null
            higher_quality_recovery_action = [string](Get-Field $Details 'recovery_action')
            detected_format = 'wxgf'
            remote_descriptor_status = $RemoteStatus
            remote_descriptor_parse_status = $RemoteParseStatus
            remote_protocol_status = $ProtocolStatus
            remote_acquisition_status = $AcquisitionStatus
            network_access_performed = $false
            content_digest_included = $false
            evidence_id_included = $false
        }
    }
}

function Assert-ImageProbe {
    param(
        [Parameter(Mandatory = $true)][object]$Invocation,
        [Parameter(Mandatory = $true)][string]$EvidenceId,
        [Parameter(Mandatory = $true)][string]$OutputPath,
        [string]$ExpectedFixture
    )
    $Envelope = Assert-SucceededEnvelope $Invocation 'image_probe_failed'
    $Data = Get-Field $Envelope 'data'
    $Meta = Get-Field $Envelope 'meta'
    Assert-KnownProbeEnums $Data
    Assert-Acceptance ([string](Get-Field $Data 'evidence_id') -ceq $EvidenceId) 'image_evidence_binding_changed'
    Assert-Acceptance ((Get-Field $Data 'network_access_performed') -eq $false) 'chat_image_network_boundary_failed'
    Assert-Acceptance ([string](Get-Field $Data 'source') -ceq 'verified_local_chat_image') 'image_source_failed'
    Assert-Acceptance ([string](Get-Field $Data 'resolution_status') -ceq 'verified_local') 'image_resolution_failed'
    Assert-Acceptance ([string](Get-Field $Data 'verified_by') -ceq 'message_resource_stem+hardlink_map+full_decode') 'image_binding_proof_failed'
    Assert-Acceptance ([string](Get-Field $Data 'container_validation') -ceq 'full_decode') 'image_container_validation_failed'
    Assert-Acceptance (-not [string]::IsNullOrWhiteSpace([string](Get-Field $Meta 'generation_id'))) 'image_generation_missing'
    Assert-Acceptance (-not [string]::IsNullOrWhiteSpace([string](Get-Field $Meta 'snapshot_manifest_sha256'))) 'image_manifest_missing'
    $Item = Get-Item -LiteralPath $OutputPath -Force -ErrorAction Stop
    Assert-Acceptance ((-not $Item.PSIsContainer) -and (($Item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -eq 0)) 'image_output_type_invalid'
    Assert-Acceptance ([int64](Get-Field $Data 'bytes') -eq [int64]$Item.Length) 'image_output_size_mismatch'
    $ActualHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $OutputPath -ErrorAction Stop).Hash.ToLowerInvariant()
    Assert-Acceptance ($ActualHash -ceq ([string](Get-Field $Data 'sha256')).ToLowerInvariant()) 'image_output_digest_mismatch'
    if (-not [string]::IsNullOrWhiteSpace($ExpectedFixture)) {
        Assert-FixtureState $ExpectedFixture $Data
    }
    return [pscustomobject]@{
        Envelope = $Envelope
        Data = $Data
        LocalResolutionStatus = 'verified_local'
        Generation = [string](Get-Field $Meta 'generation_id')
        Manifest = [string](Get-Field $Meta 'snapshot_manifest_sha256')
        Public = [ordered]@{
            fixture = $ExpectedFixture
            status = 'pass'
            quality_tier = [string](Get-Field $Data 'quality_tier')
            quality_claim_scope = [string](Get-Field $Data 'quality_claim_scope')
            source_original_dimensions_known = $false
            source_original_quality_status = 'unknown'
            dimensions_role = [string](Get-Field $Data 'dimensions_role')
            dimensions_are_observational_not_quality_gate = $true
            width = [int](Get-Field $Data 'width')
            height = [int](Get-Field $Data 'height')
            bytes = [int64](Get-Field $Data 'bytes')
            local_resolution_status = 'verified_local'
            higher_quality_local_status = [string](Get-Field $Data 'higher_quality_local_status')
            higher_quality_recovery_action = [string](Get-Field $Data 'higher_quality_recovery_action')
            remote_descriptor_status = [string](Get-Field $Data 'remote_descriptor_status')
            remote_descriptor_parse_status = [string](Get-Field $Data 'remote_descriptor_parse_status')
            remote_protocol_status = [string](Get-Field $Data 'remote_protocol_status')
            remote_acquisition_status = [string](Get-Field $Data 'remote_acquisition_status')
            network_access_performed = $false
            content_digest_included = $false
            evidence_id_included = $false
        }
    }
}

function Invoke-ImageProbe {
    param(
        [Parameter(Mandatory = $true)][string]$Executable,
        [Parameter(Mandatory = $true)][string]$AccountName,
        [Parameter(Mandatory = $true)][object]$Definition,
        [Parameter(Mandatory = $true)][string]$OutputPath,
        [bool]$ValidateFixtureState
    )
    $Invocation = Invoke-CliJson $Executable @('export-chat-image', '--account', $AccountName, '--output', $OutputPath, $Definition.EvidenceId)
    $Expected = ''
    if ($ValidateFixtureState) {
        $Expected = $Definition.Name
    }
    if ($Invocation.ExitCode -eq 0) {
        $Probe = Assert-ImageProbe $Invocation $Definition.EvidenceId $OutputPath $Expected
    }
    else {
        Assert-Acceptance ((-not $ValidateFixtureState) -or ($Definition.Name -ceq 'wxgf_candidate')) 'image_probe_failed'
        $Probe = Assert-DecoderUnavailableProbe $Invocation $OutputPath
    }
    $Probe.Public.fixture = $Definition.Name
    return $Probe
}

function Assert-RefreshResult {
    param([Parameter(Mandatory = $true)][object]$Invocation)
    $Envelope = Assert-SucceededEnvelope $Invocation 'recovery_refresh_failed'
    $Data = Get-Field $Envelope 'data'
    $AccountState = Get-Field $Data 'account'
    $Media = Get-Field $Data 'media'
    $Database = Get-Field $Data 'database'
    $PublicationCoverage = Get-Field $Database 'publication_coverage'
    Assert-Acceptance ([string](Get-Field $Data 'status') -ceq 'ready') 'recovery_refresh_not_ready'
    Assert-Acceptance ([string](Get-Field $Data 'credential_source') -ceq 'saved_keychain') 'recovery_credential_source_failed'
    Assert-Acceptance ((Get-Field $Data 'process_access_performed') -eq $false) 'recovery_process_access_failed'
    Assert-Acceptance ((Get-Field $Data 'secrets_persisted') -eq $false) 'recovery_secret_write_failed'
    Assert-Acceptance ([string](Get-Field $Media 'status') -ceq 'verified') 'recovery_media_validation_failed'
    Assert-Acceptance (($null -ne $PublicationCoverage) -and
        ([int](Get-Field $PublicationCoverage 'missing_previous') -eq 0)) 'recovery_database_coverage_regressed'
    $Generation = [string](Get-Field $AccountState 'generation_id')
    $Manifest = [string](Get-Field $AccountState 'snapshot_manifest_sha256')
    Assert-Acceptance (-not [string]::IsNullOrWhiteSpace($Generation)) 'recovery_generation_missing'
    Assert-Acceptance (-not [string]::IsNullOrWhiteSpace($Manifest)) 'recovery_manifest_missing'
    return [pscustomobject]@{ Generation = $Generation; Manifest = $Manifest }
}

function Invoke-SingleRecovery {
    param(
        [Parameter(Mandatory = $true)][string]$Executable,
        [Parameter(Mandatory = $true)][string]$AccountName,
        [Parameter(Mandatory = $true)][object]$Definition,
        [Parameter(Mandatory = $true)][string]$PrivateDirectory,
        [Parameter(Mandatory = $true)][string]$Mode,
        [Parameter(Mandatory = $true)][string]$ExpectedGeneration,
        [Parameter(Mandatory = $true)][string]$ExpectedManifest
    )
    $Result = [ordered]@{
        fixture = $Definition.Name
        confirmation_requested = $true
        user_confirmed = $false
        refresh_attempts = 0
        automatic_retry_attempts = 0
        maximum_automatic_retries = 1
        network_access_performed = $false
        outcome = 'awaiting_user_confirmation'
    }
    $PreflightOutput = Join-Path $PrivateDirectory "$($Definition.Name)-pre-recovery.bin"
    $Preflight = Invoke-ImageProbe $Executable $AccountName $Definition $PreflightOutput $true
    Assert-Acceptance (($Preflight.Generation -ceq $ExpectedGeneration) -and
        ($Preflight.Manifest -ceq $ExpectedManifest)) 'recovery_preflight_generation_mismatch'
    if ($Mode -ceq 'Skip') {
        return [pscustomobject]@{ Public = $Result; Probe = $null; Refresh = $null; Inconclusive = $true }
    }
    $ConfirmationToken = "OPENED-$($Definition.Name)"
    Write-Host "请只在微信中打开夹具 $($Definition.Name) 对应的那一张原图。完成后输入 $ConfirmationToken；其它输入将跳过恢复。"
    $Answer = Read-Host '确认'
    if ($Answer -cne $ConfirmationToken) {
        $Result.outcome = 'user_confirmation_not_received'
        return [pscustomobject]@{ Public = $Result; Probe = $null; Refresh = $null; Inconclusive = $true }
    }
    $Result.user_confirmed = $true
    $RefreshInvocation = Invoke-CliJson $Executable @('refresh', '--account', $AccountName, '--require-media')
    $Refresh = Assert-RefreshResult $RefreshInvocation
    $Result.refresh_attempts = 1
    $RetryOutput = Join-Path $PrivateDirectory "$($Definition.Name)-after-single-refresh.bin"
    $Probe = Invoke-ImageProbe $Executable $AccountName $Definition $RetryOutput $false
    $Result.automatic_retry_attempts = 1
    Assert-Acceptance (($Probe.Generation -ceq $Refresh.Generation) -and ($Probe.Manifest -ceq $Refresh.Manifest)) 'recovery_generation_binding_failed'
    if ($Probe.LocalResolutionStatus -ceq 'decoder_unavailable') {
        $Result.outcome = 'stop_after_single_refresh_decoder_unavailable'
        return [pscustomobject]@{ Public = $Result; Probe = $Probe; Refresh = $Refresh; Inconclusive = $true }
    }
    $HigherStatus = [string](Get-Field $Probe.Data 'higher_quality_local_status')
    $Quality = [string](Get-Field $Probe.Data 'quality_tier')
    $Inconclusive = $false
    switch ($Quality) {
        'high' {
            $Result.outcome = 'recovered_verified_high_cache_tier'
            if ($Definition.Name -ceq 'expiry_unknown_descriptor') {
                $Inconclusive = $true
                $Result.outcome = 'descriptor_was_not_demonstrably_unavailable'
            }
        }
        default {
            if ($HigherStatus -ceq 'missing') {
                $Result.outcome = 'stop_after_single_refresh_remote_may_be_expired_or_unavailable'
            }
            elseif ($HigherStatus -ceq 'decoder_unavailable') {
                $Result.outcome = 'stop_after_single_refresh_decoder_unavailable'
            }
            else {
                Stop-Acceptance 'recovery_retry_state_invalid'
            }
            if (($Definition.Name -ceq 'lower_tier_missing') -or
                (($Definition.Name -ceq 'expiry_unknown_descriptor') -and ($HigherStatus -cne 'missing'))) {
                $Inconclusive = $true
            }
        }
    }
    return [pscustomobject]@{ Public = $Result; Probe = $Probe; Refresh = $Refresh; Inconclusive = $Inconclusive }
}

function Write-PrivateJson {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][object]$Value
    )
    $Json = $Value | ConvertTo-Json -Depth 12
    $Encoding = [System.Text.UTF8Encoding]::new($false)
    $Bytes = $Encoding.GetBytes($Json + [Environment]::NewLine)
    $Stream = [System.IO.File]::Open($Path, [System.IO.FileMode]::CreateNew, [System.IO.FileAccess]::Write, [System.IO.FileShare]::None)
    try {
        $Stream.Write($Bytes, 0, $Bytes.Length)
        $Stream.Flush($true)
    }
    finally {
        $Stream.Dispose()
    }
}

function New-MockProbeData {
    param(
        [string]$Quality,
        [string]$HigherStatus,
        [string]$RecoveryAction,
        [string]$RemoteStatus = 'missing',
        [string]$ProtocolStatus = 'not_applicable',
        [string]$AcquisitionStatus = 'not_available_no_descriptor',
        [string]$HigherFormat = '',
        [string]$RemoteParseStatus = '',
        [int]$Width = 2560,
        [int]$Height = 1440
    )
    if ([string]::IsNullOrWhiteSpace($RemoteParseStatus)) {
        $RemoteParseStatus = switch ($RemoteStatus) {
            'present_expiry_unknown' { 'parsed_unverified_protocol' }
            'missing' { 'not_applicable' }
            default { 'not_evaluated' }
        }
    }
    return [pscustomobject]@{
        quality_tier = $Quality
        quality_basis = 'hardlink_cache_filename_variant'
        quality_claim_scope = 'wechat_cache_variant_only'
        source_original_dimensions_known = $false
        source_original_quality_status = 'unknown'
        dimensions_role = 'decoded_output_observation_not_quality_gate'
        higher_quality_local_status = $HigherStatus
        higher_quality_recovery_action = $RecoveryAction
        higher_quality_detected_format = $HigherFormat
        remote_descriptor_status = $RemoteStatus
        remote_descriptor_parse_status = $RemoteParseStatus
        remote_protocol_status = $ProtocolStatus
        remote_acquisition_status = $AcquisitionStatus
        width = $Width
        height = $Height
        bytes = 4096
        format = 'png'
    }
}

function Invoke-SelfTest {
    $Cases = @(
        [pscustomobject]@{ Name = 'lower_tier_missing'; Data = (New-MockProbeData 'medium' 'missing' 'ask_user_to_open_original_then_refresh_and_retry') },
        [pscustomobject]@{ Name = 'decodable_high'; Data = (New-MockProbeData 'high' 'not_applicable' 'none' -Width 320 -Height 240) },
        [pscustomobject]@{ Name = 'wxgf_candidate'; Data = (New-MockProbeData 'medium' 'decoder_unavailable' 'do_not_request_redownload_same_candidate' -HigherFormat 'wxgf') },
        [pscustomobject]@{ Name = 'expiry_unknown_descriptor'; Data = (New-MockProbeData 'medium' 'missing' 'ask_user_to_open_original_then_refresh_and_retry' 'present_expiry_unknown' 'unverified_desktop_protocol' 'unavailable_unverified_protocol') }
    )
    foreach ($Case in $Cases) {
        Assert-KnownProbeEnums $Case.Data
        Assert-FixtureState $Case.Name $Case.Data
    }
    $Rejected = $false
    try {
        $Unsafe = New-MockProbeData 'thumbnail' 'missing' 'ask_user_to_open_original_then_refresh_and_retry' 'verified_at_request_time' 'unverified_desktop_protocol' 'unavailable_unverified_protocol'
        Assert-FixtureState 'expiry_unknown_descriptor' $Unsafe
    }
    catch {
        $Rejected = $_.Exception.Message -ceq 'acceptance:expiry_unknown_descriptor_contract_failed'
    }
    Assert-Acceptance $Rejected 'self_test_expiry_unknown_descriptor_overclaim_not_rejected'
    $Rejected = $false
    try {
        Assert-FixtureState 'lower_tier_missing' (New-MockProbeData 'high' 'missing' 'ask_user_to_open_original_then_refresh_and_retry')
    }
    catch {
        $Rejected = $_.Exception.Message -ceq 'acceptance:lower_tier_missing_contract_failed'
    }
    Assert-Acceptance $Rejected 'self_test_high_misreported_as_lower_tier'
    $MockFailure = [pscustomobject]@{
        ExitCode = 5
        Envelope = [pscustomobject]@{
            schema_version = 1
            command_status = 'failed'
            error = [pscustomobject]@{
                type = 'chat_image_unavailable'
                details = [pscustomobject]@{
                    local_resolution_status = 'decoder_unavailable'
                    recovery_action = 'do_not_request_redownload_same_candidate'
                    detected_format = 'wxgf'
                    quality_tier = 'medium'
                    quality_basis = 'hardlink_cache_filename_variant'
                    quality_claim_scope = 'wechat_cache_variant_only'
                    source_original_dimensions_known = $false
                    source_original_quality_status = 'unknown'
                    remote_descriptor_status = 'present_expiry_unknown'
                    remote_descriptor_parse_status = 'parsed_unverified_protocol'
                    remote_protocol_status = 'unverified_desktop_protocol'
                    remote_acquisition_status = 'unavailable_unverified_protocol'
                    network_access_performed = $false
                }
            }
            meta = [pscustomobject]@{
                generation_id = 'self-test-generation'
                snapshot_manifest_sha256 = 'self-test-manifest'
            }
        }
    }
    $MissingOutput = Join-Path ([System.IO.Path]::GetTempPath()) ("v-local-self-test-" + [Guid]::NewGuid().ToString('N') + '.bin')
    $FailureProbe = Assert-DecoderUnavailableProbe $MockFailure $MissingOutput
    Assert-Acceptance (($FailureProbe.LocalResolutionStatus -ceq 'decoder_unavailable') -and
        ($FailureProbe.Generation -ceq 'self-test-generation')) 'self_test_wxgf_error_not_generation_bound'
    $Public = [ordered]@{
        schema_version = 1
        maximum_automatic_retries = 1
        contains_account = $false
        contains_evidence_ids = $false
        contains_content_digests = $false
        network_access_performed = $false
        fixed_dimension_quality_gate = $false
    }
    $Serialized = $Public | ConvertTo-Json -Compress
    foreach ($PrivateValue in @('private-account', 'wechat:private:1', 'opaque-token')) {
        Assert-Acceptance (-not $Serialized.Contains($PrivateValue)) 'self_test_private_value_leaked'
    }
    [ordered]@{ self_test = 'passed'; fixture_contracts = 4; maximum_automatic_retries = 1; network = $false } |
        ConvertTo-Json -Compress
}

if ($SelfTest) {
    Invoke-SelfTest
    exit 0
}

$Report = [ordered]@{
    schema_version = 1
    run_status = 'fail'
    failure_code = $null
    generated_at_utc = [DateTime]::UtcNow.ToString('o')
    environment = [ordered]@{}
    snapshot = [ordered]@{
        initial_generation_consistent = $false
        final_generation_consistent = $false
        generation_changed_by_recovery = $false
        generation_id_included = $false
        manifest_digest_included = $false
    }
    initial_matrix = @()
    recovery = @()
    final_probe = @()
    recovery_matrix = [ordered]@{
        lower_tier_missing = $false
        distinct_decodable_high_selected = $false
        wxgf_candidate_not_misreported_missing = $false
        descriptor_freshness_not_overstated = $false
        fixed_dimension_quality_gate = $false
        maximum_automatic_retries = 1
    }
    privacy = [ordered]@{
        contains_account = $false
        contains_evidence_ids = $false
        contains_content_digests = $false
        contains_source_paths = $false
        contains_urls_tokens_or_keys = $false
        private_artifacts_uploaded = $false
    }
}

$ReportPath = $null
$PrivateDirectory = $null
$EvidenceDirectory = $null
$FinalExitCode = 1
try {
    Assert-Acceptance ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) 'windows_required'
    Assert-Acceptance ($PSVersionTable.PSVersion.Major -ge 7) 'powershell_7_required'
    $Architecture = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
    Assert-Acceptance ($Architecture -ceq 'x64') 'windows_amd64_required'
    Assert-Acceptance (-not [string]::IsNullOrWhiteSpace($Cli)) 'cli_required'
    $ResolvedCli = (Resolve-Path -LiteralPath $Cli -ErrorAction Stop).Path
    $CliItem = Get-Item -LiteralPath $ResolvedCli -Force -ErrorAction Stop
    Assert-Acceptance ((-not $CliItem.PSIsContainer) -and (($CliItem.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -eq 0)) 'cli_file_invalid'
    Assert-PrivateIdentifier $Account 'account_invalid'
    foreach ($EvidenceId in @($LowerTierMissingEvidenceId, $DecodableHighEvidenceId, $WxgfCandidateEvidenceId, $ExpiryUnknownDescriptorEvidenceId)) {
        Assert-PrivateIdentifier $EvidenceId 'evidence_id_invalid'
        Assert-Acceptance ($EvidenceId.StartsWith('wechat:', [System.StringComparison]::Ordinal)) 'evidence_id_invalid'
    }
    $UniqueEvidence = @($LowerTierMissingEvidenceId, $DecodableHighEvidenceId, $WxgfCandidateEvidenceId, $ExpiryUnknownDescriptorEvidenceId) |
        Sort-Object -Unique
    Assert-Acceptance ($UniqueEvidence.Count -eq 4) 'fixture_evidence_ids_not_distinct'
    if ([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
        Stop-Acceptance 'local_app_data_unavailable'
    }
    if ([string]::IsNullOrWhiteSpace($PrivateRootBase)) {
        $PrivateRootBase = Join-Path $env:LOCALAPPDATA 'v-local\acceptance-private'
    }
    if ([string]::IsNullOrWhiteSpace($EvidenceRootBase)) {
        $EvidenceRootBase = Join-Path $env:LOCALAPPDATA 'v-local\acceptance-evidence'
    }
    $RunId = [DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss') + '-' + [Guid]::NewGuid().ToString('N').Substring(0, 8)
    $PrivateDirectory = New-PrivateRunDirectory $PrivateRootBase $RunId 'private_output_directory_invalid'
    $EvidenceDirectory = New-PrivateRunDirectory $EvidenceRootBase $RunId 'evidence_output_directory_invalid'
    $ReportPath = Join-Path $EvidenceDirectory 'w64-08-chat-image-recovery.json'

    try {
        $VersionOutput = @(& $ResolvedCli '--version' 2>&1)
        $VersionExitCode = $LASTEXITCODE
    }
    catch {
        Stop-Acceptance 'cli_version_failed'
    }
    $CliVersion = (($VersionOutput | ForEach-Object { $_.ToString() }) -join '').Trim()
    Assert-Acceptance (($VersionExitCode -eq 0) -and ($CliVersion -match '^[0-9A-Za-z._+-]{1,64}$')) 'cli_version_invalid'
    $Report.environment = [ordered]@{
        windows_build = [Environment]::OSVersion.Version.ToString()
        host_arch = 'amd64'
        powershell_version = $PSVersionTable.PSVersion.ToString()
        cli_version = $CliVersion
        cli_sha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $ResolvedCli -ErrorAction Stop).Hash.ToLowerInvariant()
        cli_path_included = $false
    }

    $Definitions = @(
        [pscustomobject]@{ Name = 'lower_tier_missing'; EvidenceId = $LowerTierMissingEvidenceId },
        [pscustomobject]@{ Name = 'decodable_high'; EvidenceId = $DecodableHighEvidenceId },
        [pscustomobject]@{ Name = 'wxgf_candidate'; EvidenceId = $WxgfCandidateEvidenceId },
        [pscustomobject]@{ Name = 'expiry_unknown_descriptor'; EvidenceId = $ExpiryUnknownDescriptorEvidenceId }
    )
    $InitialProbes = @()
    foreach ($Definition in $Definitions) {
        $OutputPath = Join-Path $PrivateDirectory "$($Definition.Name)-initial.bin"
        $InitialProbes += Invoke-ImageProbe $ResolvedCli $Account $Definition $OutputPath $true
    }
    $InitialGenerations = @($InitialProbes | ForEach-Object { $_.Generation } | Sort-Object -Unique)
    $InitialManifests = @($InitialProbes | ForEach-Object { $_.Manifest } | Sort-Object -Unique)
    Assert-Acceptance (($InitialGenerations.Count -eq 1) -and ($InitialManifests.Count -eq 1)) 'initial_generation_mismatch'
    $Report.snapshot.initial_generation_consistent = $true
    $Report.initial_matrix = @($InitialProbes | ForEach-Object { $_.Public })
    $Report.recovery_matrix.lower_tier_missing = $true
    $Report.recovery_matrix.distinct_decodable_high_selected = $true
    $Report.recovery_matrix.wxgf_candidate_not_misreported_missing = $true
    $Report.recovery_matrix.descriptor_freshness_not_overstated = $true

    $RecoveryResults = @()
    $Inconclusive = $false
    $LastRefresh = $null
    $CurrentGeneration = $InitialGenerations[0]
    $CurrentManifest = $InitialManifests[0]
    foreach ($Definition in @($Definitions[0], $Definitions[3])) {
        $Recovery = Invoke-SingleRecovery $ResolvedCli $Account $Definition $PrivateDirectory $RecoveryMode $CurrentGeneration $CurrentManifest
        $RecoveryResults += $Recovery
        if ($Recovery.Inconclusive) {
            $Inconclusive = $true
        }
        if ($null -ne $Recovery.Refresh) {
            Assert-Acceptance (($Recovery.Refresh.Generation -cne $CurrentGeneration) -or
                ($Recovery.Refresh.Manifest -cne $CurrentManifest)) 'recovery_did_not_publish_new_generation'
            $LastRefresh = $Recovery.Refresh
            $CurrentGeneration = $Recovery.Refresh.Generation
            $CurrentManifest = $Recovery.Refresh.Manifest
        }
    }
    $Report.recovery = @($RecoveryResults | ForEach-Object { $_.Public })
    $Report.snapshot.generation_changed_by_recovery = $null -ne $LastRefresh

    $FinalProbes = @()
    foreach ($Definition in $Definitions) {
        $OutputPath = Join-Path $PrivateDirectory "$($Definition.Name)-final.bin"
        $ValidateFinalState = @('decodable_high', 'wxgf_candidate') -ccontains $Definition.Name
        $FinalProbes += Invoke-ImageProbe $ResolvedCli $Account $Definition $OutputPath $ValidateFinalState
    }
    $FinalGenerations = @($FinalProbes | ForEach-Object { $_.Generation } | Sort-Object -Unique)
    $FinalManifests = @($FinalProbes | ForEach-Object { $_.Manifest } | Sort-Object -Unique)
    Assert-Acceptance (($FinalGenerations.Count -eq 1) -and ($FinalManifests.Count -eq 1)) 'final_generation_mismatch'
    if ($null -ne $LastRefresh) {
        Assert-Acceptance (($FinalGenerations[0] -ceq $LastRefresh.Generation) -and ($FinalManifests[0] -ceq $LastRefresh.Manifest)) 'final_generation_not_latest_refresh'
    }
    else {
        Assert-Acceptance (($FinalGenerations[0] -ceq $InitialGenerations[0]) -and ($FinalManifests[0] -ceq $InitialManifests[0])) 'final_generation_changed_without_refresh'
    }
    $Report.snapshot.final_generation_consistent = $true
    $Report.final_probe = @($FinalProbes | ForEach-Object {
        [ordered]@{
            fixture = $_.Public.fixture
            status = $_.Public.status
            quality_tier = $_.Public.quality_tier
            source_original_quality_status = $_.Public.source_original_quality_status
            local_resolution_status = $_.Public.local_resolution_status
            higher_quality_local_status = $_.Public.higher_quality_local_status
            remote_descriptor_status = $_.Public.remote_descriptor_status
            remote_protocol_status = $_.Public.remote_protocol_status
            remote_acquisition_status = $_.Public.remote_acquisition_status
            network_access_performed = $false
            evidence_id_included = $false
            content_digest_included = $false
        }
    })
    if ($Inconclusive) {
        $Report.run_status = 'inconclusive'
        $FinalExitCode = 2
    }
    else {
        $Report.run_status = 'pass'
        $FinalExitCode = 0
    }
}
catch {
    $FailureMessage = [string]$_.Exception.Message
    if ($FailureMessage.StartsWith('acceptance:', [System.StringComparison]::Ordinal)) {
        $Report.failure_code = $FailureMessage.Substring('acceptance:'.Length)
    }
    else {
        $Report.failure_code = 'unexpected_script_failure'
    }
    $Report.run_status = 'fail'
    $FinalExitCode = 1
}

if ($null -ne $ReportPath) {
    try {
        Write-PrivateJson $ReportPath $Report
    }
    catch {
        $Report.run_status = 'fail'
        $Report.failure_code = 'sanitized_report_write_failed'
        $FinalExitCode = 1
    }
}

$ConsoleSummary = [ordered]@{
    run_status = $Report.run_status
    failure_code = $Report.failure_code
    sanitized_report_created = ($null -ne $ReportPath -and (Test-Path -LiteralPath $ReportPath))
    private_artifacts_retained_locally = ($null -ne $PrivateDirectory)
    sanitized_report_filename = 'w64-08-chat-image-recovery.json'
}
if ($ShowPaths) {
    $ConsoleSummary.report_path = $ReportPath
    $ConsoleSummary.private_artifact_directory = $PrivateDirectory
}
$ConsoleSummary | ConvertTo-Json -Compress
exit $FinalExitCode
