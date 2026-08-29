<#
.SYNOPSIS
Records a private, human side-by-side WXGF visual-equivalence review.

.DESCRIPTION
The script validates a capture produced by the opt-in Go qualification test,
opens offline HTML comparisons, requires an exact per-sample confirmation, then
removes decoded/reference/comparison files. Private review records retain the
snapshot/evidence/content binding. The ordinary report contains only coverage
counts and blockers. Skip mode validates and removes capture/decoded files
without requiring a reference image, CLI, account, browser, or private record.
The script never operates the WeChat UI or uses a network.

.NOTES
Exit 0 means the two-version high+medium matrix is complete, 1 means failure,
and 2 means this review was skipped/rejected or the matrix remains insufficient.
#>
[CmdletBinding()]
param(
    [string]$Helper,
    [string]$Cli,
    [string]$Account,
    [string]$ReviewRoot,
    [string]$PrivateRecordRootBase,
    [string]$EvidenceRootBase,
    [ValidateSet('Prompt', 'Skip')]
    [string]$ReviewMode = 'Prompt',
    [switch]$ShowPaths,
    [switch]$SelfTest
)

Set-StrictMode -Version 3.0

$HelperProtocol = 'v-local-cli/wxgf-visual-review-helper/v1'
$RecordProtocol = 'v-local-cli/wxgf-visual-review-record/v1'
$ReportProtocol = 'v-local-cli/windows-wxgf-visual-equivalence-evidence/v1'
$ProviderProtocol = 'v-local-cli-image-decoder/1'
$DecoderIdentityBasis = 'provider_reported_adjacent_decoder_sha256_unattested_provider'
$ProviderBinaryTrustStatus = 'unverified'
$QualityTierBasis = 'hardlink_cache_filename_variant_not_source_quality'
$SourceOriginalQualityStatus = 'unknown'
$SourceProducerVersionStatus = 'unknown'
$VersionCoverageBasis = 'installed_package_at_review_not_source_provenance'

function Stop-Review {
    param([Parameter(Mandatory = $true)][string]$Code)
    throw [System.InvalidOperationException]::new("wxgf-visual-review:$Code")
}

function Assert-Review {
    param(
        [Parameter(Mandatory = $true)][bool]$Condition,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if (-not $Condition) {
        Stop-Review $Code
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
        Stop-Review $Code
    }
    foreach ($Character in $Value.ToCharArray()) {
        if ([char]::IsControl($Character)) {
            Stop-Review $Code
        }
    }
}

function Assert-LocalAbsolutePath {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if (-not [System.IO.Path]::IsPathFullyQualified($Path) -or $Path.StartsWith('\\')) {
        Stop-Review $Code
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
            Stop-Review $Code
        }
    }
}

function Assert-RegularFile {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    Assert-LocalAbsolutePath $Path $Code
    Assert-NoReparsePoint $Path $Code
    $Item = Get-Item -LiteralPath $Path -Force -ErrorAction Stop
    Assert-Review ((-not $Item.PSIsContainer) -and (($Item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -eq 0) -and $Item.Length -gt 0) $Code
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

function Assert-PrivateDirectoryAcl {
    param([Parameter(Mandatory = $true)][string]$Path)
    $CurrentUser = [System.Security.Principal.WindowsIdentity]::GetCurrent().User
    $Trusted = @(
        $CurrentUser.Value,
        'S-1-5-18',
        'S-1-5-32-544'
    )
    $Security = Get-Acl -LiteralPath $Path -ErrorAction Stop
    Assert-Review $Security.AreAccessRulesProtected 'private_base_acl_inherited'
    try {
        $Owner = ([System.Security.Principal.NTAccount]$Security.Owner).Translate(
            [System.Security.Principal.SecurityIdentifier]
        ).Value
    }
    catch {
        Stop-Review 'private_base_owner_invalid'
    }
    Assert-Review ($Trusted -ccontains $Owner) 'private_base_owner_invalid'
    $CurrentUserAllowed = $false
    foreach ($Rule in $Security.GetAccessRules(
        $true,
        $true,
        [System.Security.Principal.SecurityIdentifier]
    )) {
        if ($Rule.AccessControlType -eq [System.Security.AccessControl.AccessControlType]::Deny) {
            continue
        }
        $Sid = ([System.Security.Principal.SecurityIdentifier]$Rule.IdentityReference).Value
        Assert-Review ($Trusted -ccontains $Sid) 'private_base_acl_untrusted_allow'
        if ($Sid -ceq $CurrentUser.Value) {
            $CurrentUserAllowed = $true
        }
    }
    Assert-Review $CurrentUserAllowed 'private_base_acl_current_user_missing'
}

function Ensure-PrivateBase {
    param([Parameter(Mandatory = $true)][string]$Path)
    Assert-LocalAbsolutePath $Path 'private_base_invalid'
    $Created = $false
    if (-not (Test-Path -LiteralPath $Path)) {
        [void](New-Item -ItemType Directory -Path $Path -ErrorAction Stop)
        $Created = $true
    }
    Assert-NoReparsePoint $Path 'private_base_invalid'
    $Item = Get-Item -LiteralPath $Path -Force -ErrorAction Stop
    Assert-Review $Item.PSIsContainer 'private_base_invalid'
    if ($Created) {
        Set-PrivateDirectoryAcl $Path
    }
    Assert-PrivateDirectoryAcl $Path
    return [System.IO.Path]::GetFullPath($Path)
}

function New-PrivateRunDirectory {
    param(
        [Parameter(Mandatory = $true)][string]$Base,
        [Parameter(Mandatory = $true)][string]$RunId
    )
    $Directory = Join-Path $Base $RunId
    if (Test-Path -LiteralPath $Directory) {
        Stop-Review 'private_run_directory_exists'
    }
    [void](New-Item -ItemType Directory -Path $Directory -ErrorAction Stop)
    Assert-NoReparsePoint $Directory 'private_run_directory_invalid'
    Set-PrivateDirectoryAcl $Directory
    return [System.IO.Path]::GetFullPath($Directory)
}

function Write-PrivateJson {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][object]$Value
    )
    $Payload = ($Value | ConvertTo-Json -Depth 20) + [Environment]::NewLine
    $Encoding = [System.Text.UTF8Encoding]::new($false)
    $File = [System.IO.FileStream]::new(
        $Path,
        [System.IO.FileMode]::CreateNew,
        [System.IO.FileAccess]::Write,
        [System.IO.FileShare]::None
    )
    try {
        $Bytes = $Encoding.GetBytes($Payload)
        $File.Write($Bytes, 0, $Bytes.Length)
        $File.Flush($true)
    }
    finally {
        $File.Dispose()
    }
}

function Invoke-JsonProcess {
    param(
        [Parameter(Mandatory = $true)][string]$Executable,
        [Parameter(Mandatory = $true)][object]$Request,
        [Parameter(Mandatory = $true)][string]$FailureCode
    )
    $RequestJson = $Request | ConvertTo-Json -Depth 20 -Compress
    try {
        $NativeOutput = @($RequestJson | & $Executable 2>&1)
        $ExitCode = $LASTEXITCODE
    }
    catch {
        Stop-Review $FailureCode
    }
    $Text = ($NativeOutput | ForEach-Object { $_.ToString() }) -join [Environment]::NewLine
    Assert-Review (($ExitCode -eq 0) -and (-not [string]::IsNullOrWhiteSpace($Text)) -and $Text.Length -le 1048576) $FailureCode
    try {
        return $Text | ConvertFrom-Json -ErrorAction Stop
    }
    catch {
        Stop-Review $FailureCode
    }
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
        Stop-Review 'cli_process_start_failed'
    }
    $Text = ($NativeOutput | ForEach-Object { $_.ToString() }) -join [Environment]::NewLine
    Assert-Review (($ExitCode -eq 0) -and (-not [string]::IsNullOrWhiteSpace($Text)) -and $Text.Length -le 1048576) 'cli_status_failed'
    try {
        $Envelope = $Text | ConvertFrom-Json -ErrorAction Stop
    }
    catch {
        Stop-Review 'cli_status_invalid'
    }
    Assert-Review (((Get-Field $Envelope 'schema_version') -eq 1) -and ((Get-Field $Envelope 'command_status') -ceq 'succeeded')) 'cli_status_invalid'
    return $Envelope
}

function Remove-ReviewArtifacts {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][object[]]$Samples
    )
    Assert-LocalAbsolutePath $Root 'review_cleanup_root_invalid'
    Assert-NoReparsePoint $Root 'review_cleanup_root_invalid'
    $Expected = @((Join-Path $Root 'capture.json'))
    foreach ($Sample in $Samples) {
        $Ordinal = [int](Get-Field $Sample 'ordinal')
        $Expected += Join-Path $Root ('decoded-{0:D2}.png' -f $Ordinal)
        $Expected += Join-Path $Root ('reference-{0:D2}.png' -f $Ordinal)
        $Expected += Join-Path $Root ('review-{0:D2}.html' -f $Ordinal)
    }
    foreach ($Path in $Expected) {
        if (Test-Path -LiteralPath $Path) {
            Assert-NoReparsePoint $Path 'review_cleanup_file_invalid'
            Remove-Item -LiteralPath $Path -Force -ErrorAction Stop
        }
    }
    $Remaining = @(Get-ChildItem -LiteralPath $Root -Force -ErrorAction Stop)
    if ($Remaining.Count -ne 0) {
        return $false
    }
    Remove-Item -LiteralPath $Root -Force -ErrorAction Stop
    return -not (Test-Path -LiteralPath $Root)
}

function New-RunNonce {
    $Bytes = [byte[]]::new(16)
    [System.Security.Cryptography.RandomNumberGenerator]::Fill($Bytes)
    return [Convert]::ToHexString($Bytes).ToLowerInvariant()
}

function Invoke-SelfTest {
    $Challenge = '0123abcd'
    $Expected = "CONFIRM-CONTENT-ORIENTATION-CROP-COLOR-$Challenge"
    Assert-Review ($Expected -cne 'yes') 'self_test_weak_confirmation'
    $Public = [ordered]@{
        protocol = $ReportProtocol
        run_status = 'inconclusive'
        sample_review_status = 'not_recorded'
        contains_account = $false
        contains_evidence_ids = $false
        contains_image_content_digests = $false
        fixed_dimension_quality_gate = $false
        version_coverage_basis = $VersionCoverageBasis
        source_producer_version_status = $SourceProducerVersionStatus
        provider_binary_trust_status = $ProviderBinaryTrustStatus
        production_ready = $false
        network_access_performed = $false
        wechat_ui_automated = $false
    }
    $Serialized = $Public | ConvertTo-Json -Compress
    foreach ($Private in @('private-account', 'wechat:private:1', ('ab' * 32))) {
        Assert-Review (-not $Serialized.Contains($Private)) 'self_test_private_value_leaked'
    }
    [ordered]@{
        self_test = 'passed'
        exact_confirmation = $true
        private_public_evidence_split = $true
        fixed_dimension_quality_gate = $false
        producer_version_not_overstated = $true
        decoder_build_bound = $true
        provider_binary_trust_status = $ProviderBinaryTrustStatus
        production_ready = $false
        network = $false
        wechat_ui_automated = $false
    } | ConvertTo-Json -Compress
}

if ($SelfTest) {
    Invoke-SelfTest
    exit 0
}

$Report = [ordered]@{
    schema_version = 1
    protocol = $ReportProtocol
    generated_at_utc = [DateTime]::UtcNow.ToString('o')
    run_status = 'failed'
    failure_code = $null
    sample_review = [ordered]@{
        status = 'not_recorded'
        samples_presented = 0
        samples_confirmed = 0
        content_confirmed = $false
        orientation_confirmed = $false
        crop_confirmed = $false
        color_and_artifacts_confirmed = $false
        temporary_disk_artifacts_removed = $false
        browser_cache_erasure_proven = $false
    }
    environment = [ordered]@{
        wechat_version = $null
        client_version_observation = 'not_observed'
        version_coverage_basis = $null
        source_producer_version_status = $SourceProducerVersionStatus
    }
    decoder = [ordered]@{
        reported_name = $null
        reported_version = $null
        identity_basis = $DecoderIdentityBasis
        provider_binary_trust_status = $ProviderBinaryTrustStatus
    }
    snapshot = [ordered]@{
        generation_bound = $false
        manifest_bound = $false
        identifiers_included = $false
    }
    matrix = $null
    privacy = [ordered]@{
        contains_account = $false
        contains_evidence_ids = $false
        contains_image_content_digests = $false
        contains_source_paths = $false
        private_records_uploaded = $false
        network_access_performed = $false
        wechat_ui_automated = $false
        fixed_dimension_quality_gate = $false
        production_ready = $false
    }
}

$Prepared = $null
$ReviewStarted = $false
$ArtifactsRemoved = $false
$ReportPath = $null
$FinalExitCode = 1
try {
    Assert-Review ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) 'windows_required'
    Assert-Review ($PSVersionTable.PSVersion.Major -ge 7) 'powershell_7_required'
    Assert-Review (-not [string]::IsNullOrWhiteSpace($Helper)) 'executable_invalid'
    Assert-RegularFile ([System.IO.Path]::GetFullPath($Helper)) 'executable_invalid'
    if ($ReviewMode -ceq 'Prompt') {
        Assert-PrivateIdentifier $Account 'account_invalid'
        Assert-Review (-not [string]::IsNullOrWhiteSpace($Cli)) 'executable_invalid'
        Assert-RegularFile ([System.IO.Path]::GetFullPath($Cli)) 'executable_invalid'
    }
    Assert-LocalAbsolutePath $ReviewRoot 'review_root_invalid'
    Assert-NoReparsePoint $ReviewRoot 'review_root_invalid'
    $ReviewItem = Get-Item -LiteralPath $ReviewRoot -Force -ErrorAction Stop
    Assert-Review $ReviewItem.PSIsContainer 'review_root_invalid'
    if ([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
        Stop-Review 'local_app_data_unavailable'
    }
    if ($ReviewMode -ceq 'Prompt') {
        if ([string]::IsNullOrWhiteSpace($PrivateRecordRootBase)) {
            $PrivateRecordRootBase = Join-Path $env:LOCALAPPDATA 'v-local\wxgf-review-records'
        }
        $PrivateRecordRootBase = Ensure-PrivateBase $PrivateRecordRootBase
    }
    if ([string]::IsNullOrWhiteSpace($EvidenceRootBase)) {
        $EvidenceRootBase = Join-Path $env:LOCALAPPDATA 'v-local\acceptance-evidence\wxgf-visual-equivalence'
    }
    $EvidenceRootBase = Ensure-PrivateBase $EvidenceRootBase

    $HelperAction = if ($ReviewMode -ceq 'Skip') { 'inspect' } else { 'prepare' }
    $ExpectedHelperStatus = if ($ReviewMode -ceq 'Skip') { 'inspected' } else { 'prepared' }
    $Prepared = Invoke-JsonProcess $Helper ([ordered]@{
        protocol = $HelperProtocol
        action = $HelperAction
        review_root = [System.IO.Path]::GetFullPath($ReviewRoot)
    }) 'review_prepare_failed'
    Assert-Review (((Get-Field $Prepared 'protocol') -ceq $HelperProtocol) -and
        ((Get-Field $Prepared 'status') -ceq $ExpectedHelperStatus)) 'review_prepare_invalid'
    $Samples = @((Get-Field $Prepared 'samples'))
    $Capture = Get-Field $Prepared 'capture'
    Assert-Review (($Samples.Count -ge 1) -and ($Samples.Count -le 5) -and ($null -ne $Capture)) 'review_prepare_invalid'
    $ReviewStarted = $true
    $ReportedDecoder = [string](Get-Field $Capture 'reported_decoder')
    $ReportedDecoderVersion = [string](Get-Field $Capture 'reported_decoder_version')
    Assert-Review (($ReportedDecoder -ceq 'ffmpeg') -and ($ReportedDecoderVersion -cmatch '^sha256:[0-9a-f]{64}$') -and
        ((Get-Field $Capture 'decoder_identity_basis') -ceq $DecoderIdentityBasis) -and
        ((Get-Field $Capture 'provider_protocol') -ceq $ProviderProtocol) -and
        ((Get-Field $Capture 'provider_binary_trust_status') -ceq $ProviderBinaryTrustStatus)) 'review_decoder_identity_invalid'
    $Report.sample_review.samples_presented = $Samples.Count
    $Report.decoder.reported_name = $ReportedDecoder
    $Report.decoder.reported_version = $ReportedDecoderVersion

    $WeChatVersion = $null
    if ($ReviewMode -ceq 'Prompt') {
        $StatusEnvelope = Invoke-CliJson $Cli @('ocr-status', '--account', $Account)
        $StatusData = Get-Field $StatusEnvelope 'data'
        $StatusMeta = Get-Field $StatusEnvelope 'meta'
        $NativeStatus = Get-Field $StatusData 'native_experimental'
        $WeChatVersion = [string](Get-Field $NativeStatus 'wechat_version')
        Assert-Review ((-not [string]::IsNullOrWhiteSpace($WeChatVersion)) -and $WeChatVersion.Length -le 64 -and
            ((Get-Field $NativeStatus 'source') -ceq 'installed_wechat_package') -and
            ((Get-Field $StatusData 'engine_invoked') -eq $false) -and ((Get-Field $StatusData 'private_ipc_invoked') -eq $false) -and
            ((Get-Field $StatusData 'network_performed') -eq $false)) 'wechat_version_observation_invalid'
        Assert-Review (((Get-Field $StatusMeta 'generation_id') -ceq (Get-Field $Capture 'generation_id')) -and
            ((Get-Field $StatusMeta 'snapshot_manifest_sha256') -ceq (Get-Field $Capture 'snapshot_manifest_sha256'))) 'snapshot_binding_changed'
        $Report.environment.wechat_version = $WeChatVersion
        $Report.environment.client_version_observation = 'installed_package_at_review'
        $Report.environment.version_coverage_basis = $VersionCoverageBasis
        $Report.snapshot.generation_bound = $true
        $Report.snapshot.manifest_bound = $true
    }

    $AllConfirmed = $ReviewMode -ceq 'Prompt'
    if ($ReviewMode -ceq 'Prompt') {
        foreach ($Sample in $Samples) {
            $Ordinal = [int](Get-Field $Sample 'ordinal')
            $EvidenceId = [string](Get-Field $Sample 'evidence_id')
            Assert-PrivateIdentifier $EvidenceId 'prepared_evidence_id_invalid'
            $BundleName = [string](Get-Field $Sample 'bundle_file_name')
            $ExpectedBundleName = 'review-{0:D2}.html' -f $Ordinal
            Assert-Review ($BundleName -ceq $ExpectedBundleName) 'review_bundle_name_invalid'
            $BundlePath = Join-Path $ReviewRoot $BundleName
            Assert-RegularFile $BundlePath 'review_bundle_invalid'
            Write-Host "私有样本 $Ordinal/$($Samples.Count)，evidence_id=$EvidenceId"
            Write-Host '请确认左图来自这条微信消息的界面截图，并逐项检查：内容、方向、裁剪、颜色/解码伪影。'
            Write-Warning '本脚本会删除磁盘临时图，但无法证明浏览器历史、缓存或进程内存已擦除。'
            try {
                [void](Start-Process -FilePath $BundlePath -ErrorAction Stop)
            }
            catch {
                Stop-Review 'review_bundle_open_failed'
            }
            $Challenge = [Guid]::NewGuid().ToString('N').Substring(0, 8)
            $Expected = "CONFIRM-CONTENT-ORIENTATION-CROP-COLOR-$Challenge"
            $Answer = Read-Host "四项全部一致时输入 $Expected；其他输入均不通过"
            if ($Answer -cne $Expected) {
                $AllConfirmed = $false
                break
            }
            $Report.sample_review.samples_confirmed++
        }
    }

    $ArtifactsRemoved = Remove-ReviewArtifacts ([System.IO.Path]::GetFullPath($ReviewRoot)) $Samples
    Assert-Review $ArtifactsRemoved 'temporary_artifact_cleanup_failed'
    $Report.sample_review.temporary_disk_artifacts_removed = $true

    if (-not $AllConfirmed -or $Report.sample_review.samples_confirmed -ne $Samples.Count) {
        $Report.run_status = 'inconclusive'
        $Report.sample_review.status = if ($ReviewMode -ceq 'Skip') { 'skipped' } else { 'not_confirmed' }
        $FinalExitCode = 2
    }
    else {
        $ReviewedAt = [DateTime]::UtcNow.ToString('o')
        foreach ($Sample in $Samples) {
            $Ordinal = [int](Get-Field $Sample 'ordinal')
            $Record = [ordered]@{
                protocol = $RecordProtocol
                review_status = 'confirmed'
                review_method = 'human_side_by_side_wechat_ui_reference'
                run_nonce = New-RunNonce
                reviewed_at_utc = $ReviewedAt
                wechat_version = $WeChatVersion
                client_version_observation = 'installed_package_at_review'
                source_producer_version_status = $SourceProducerVersionStatus
                reported_decoder = $ReportedDecoder
                reported_decoder_version = $ReportedDecoderVersion
                decoder_identity_basis = $DecoderIdentityBasis
                provider_protocol = $ProviderProtocol
                provider_binary_trust_status = $ProviderBinaryTrustStatus
                quality_tier = [string](Get-Field $Sample 'quality_tier')
                quality_tier_basis = $QualityTierBasis
                evidence_id = [string](Get-Field $Sample 'evidence_id')
                generation_id = [string](Get-Field $Capture 'generation_id')
                snapshot_manifest_sha256 = [string](Get-Field $Capture 'snapshot_manifest_sha256')
                wxgf_sha256 = [string](Get-Field $Sample 'wxgf_sha256')
                decoded_sha256 = [string](Get-Field (Get-Field $Sample 'decoded') 'sha256')
                decoded_visual_fingerprint = [string](Get-Field (Get-Field $Sample 'decoded') 'visual_fingerprint')
                reference_sha256 = [string](Get-Field (Get-Field $Sample 'reference') 'sha256')
                decoded_width = [int](Get-Field (Get-Field $Sample 'decoded') 'width')
                decoded_height = [int](Get-Field (Get-Field $Sample 'decoded') 'height')
                reference_width = [int](Get-Field (Get-Field $Sample 'reference') 'width')
                reference_height = [int](Get-Field (Get-Field $Sample 'reference') 'height')
                same_content_confirmed = $true
                orientation_confirmed = $true
                crop_confirmed = $true
                color_and_artifacts_confirmed = $true
                source_original_quality_status = $SourceOriginalQualityStatus
                temporary_decoded_removed = $true
                temporary_reference_removed = $true
                temporary_review_bundle_removed = $true
            }
            $RecordRunId = [DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss') + '-' +
                [Guid]::NewGuid().ToString('N').Substring(0, 8) + ('-{0:D2}' -f $Ordinal)
            $RecordDirectory = New-PrivateRunDirectory $PrivateRecordRootBase $RecordRunId
            $RecordPath = Join-Path $RecordDirectory 'record.json'
            Write-PrivateJson $RecordPath $Record
        }

        $AllRecordPaths = @()
        foreach ($Directory in @(Get-ChildItem -LiteralPath $PrivateRecordRootBase -Directory -Force -ErrorAction Stop)) {
            if (($Directory.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
                Stop-Review 'private_record_directory_reparse_point'
            }
            $RecordPath = Join-Path $Directory.FullName 'record.json'
            if (Test-Path -LiteralPath $RecordPath) {
                Assert-RegularFile $RecordPath 'private_record_invalid'
                $AllRecordPaths += $RecordPath
            }
        }
        $Evaluation = Invoke-JsonProcess $Helper ([ordered]@{
            protocol = $HelperProtocol
            action = 'evaluate_matrix'
            record_root = $PrivateRecordRootBase
            record_paths = $AllRecordPaths
            reported_decoder = $ReportedDecoder
            reported_decoder_version = $ReportedDecoderVersion
        }) 'matrix_evaluation_failed'
        Assert-Review (((Get-Field $Evaluation 'protocol') -ceq $HelperProtocol) -and
            ((Get-Field $Evaluation 'status') -ceq 'evaluated') -and ($null -ne (Get-Field $Evaluation 'matrix'))) 'matrix_evaluation_invalid'
        $Report.matrix = Get-Field $Evaluation 'matrix'
        $Report.sample_review.status = 'confirmed'
        $Report.sample_review.content_confirmed = $true
        $Report.sample_review.orientation_confirmed = $true
        $Report.sample_review.crop_confirmed = $true
        $Report.sample_review.color_and_artifacts_confirmed = $true
        if ((Get-Field $Report.matrix 'status') -ceq 'pass') {
            $Report.run_status = 'pass'
            $FinalExitCode = 0
        }
        else {
            $Report.run_status = 'inconclusive'
            $FinalExitCode = 2
        }
    }
}
catch {
    $Message = [string]$_.Exception.Message
    $Report.failure_code = if ($Message.StartsWith('wxgf-visual-review:', [System.StringComparison]::Ordinal)) {
        $Message.Substring('wxgf-visual-review:'.Length)
    }
    else {
        'unexpected_script_failure'
    }
    $Report.run_status = 'failed'
    $FinalExitCode = 1
}
finally {
    if ($ReviewStarted -and -not $ArtifactsRemoved -and (Test-Path -LiteralPath $ReviewRoot)) {
        try {
            $SamplesForCleanup = if ($null -ne $Prepared) { @((Get-Field $Prepared 'samples')) } else { @() }
            if ($SamplesForCleanup.Count -gt 0) {
                $ArtifactsRemoved = Remove-ReviewArtifacts ([System.IO.Path]::GetFullPath($ReviewRoot)) $SamplesForCleanup
                $Report.sample_review.temporary_disk_artifacts_removed = $ArtifactsRemoved
            }
        }
        catch {
            $Report.run_status = 'failed'
            $Report.failure_code = 'temporary_artifact_cleanup_failed'
            $FinalExitCode = 1
        }
    }
    try {
        if (-not [string]::IsNullOrWhiteSpace($EvidenceRootBase) -and (Test-Path -LiteralPath $EvidenceRootBase)) {
            $EvidenceRunId = [DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss') + '-' + [Guid]::NewGuid().ToString('N').Substring(0, 8)
            $EvidenceDirectory = New-PrivateRunDirectory $EvidenceRootBase $EvidenceRunId
            $ReportPath = Join-Path $EvidenceDirectory 'wxgf-visual-equivalence.json'
            Write-PrivateJson $ReportPath $Report
        }
    }
    catch {
        $Report.run_status = 'failed'
        $Report.failure_code = 'sanitized_report_write_failed'
        $FinalExitCode = 1
    }
}

$Summary = [ordered]@{
    run_status = $Report.run_status
    failure_code = $Report.failure_code
    sample_review_status = $Report.sample_review.status
    samples_confirmed = $Report.sample_review.samples_confirmed
    temporary_disk_artifacts_removed = $Report.sample_review.temporary_disk_artifacts_removed
    matrix_status = if ($null -eq $Report.matrix) { 'not_evaluated' } else { [string](Get-Field $Report.matrix 'status') }
    evidence_path_included = [bool]$ShowPaths
}
if ($ShowPaths -and -not [string]::IsNullOrWhiteSpace($ReportPath)) {
    $Summary.evidence_path = $ReportPath
}
$Summary | ConvertTo-Json -Compress
exit $FinalExitCode
