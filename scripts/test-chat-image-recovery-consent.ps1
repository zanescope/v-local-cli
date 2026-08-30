<#
.SYNOPSIS
Checks the recover-chat-image consent and privacy contract without accessing WeChat data or the network.

.DESCRIPTION
Reads only `v-local-cli schema recover-chat-image`. It verifies that recovery is offline by
default, uses a short-lived replay-protected challenge, binds one account/message/image/
generation/descriptor/output, accepts only a snapshot-provided HTTPS full URL, never follows
redirects, and keeps source original quality unknown.

.NOTES
Exit 0 means pass and 1 means contract failure. `-SelfTest` validates this script's own
assertions with an in-memory schema object and does not invoke a CLI.
#>
[CmdletBinding()]
param(
    [string]$Cli,
    [switch]$SelfTest
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = 'Stop'

function Assert-Contract {
    param(
        [Parameter(Mandatory = $true)][bool]$Condition,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if (-not $Condition) {
        throw [System.InvalidOperationException]::new("contract:$Code")
    }
}

function Test-RecoveryDefinition {
    param([Parameter(Mandatory = $true)][object]$Definition)

    Assert-Contract ($Definition.usage -match '\brecover-chat-image\b' -and $Definition.usage -match '--consent CHALLENGE') 'usage_missing_consent'
    Assert-Contract ($Definition.usage -notmatch '--allow-network') 'overlapping_allow_network_flag'
    Assert-Contract ($Definition.usage -notmatch '--(?:no-limit|limit|all|force)\b' -and $Definition.force_supported -eq $false) 'overlapping_or_unsafe_option'
    Assert-Contract ($Definition.network_default -eq $false) 'network_not_default_off'
    Assert-Contract ($Definition.account_lock -eq $true -and $Definition.account_lock_scope -ceq 'entire_offline_preflight_or_authorized_attempt') 'snapshot_transaction_lock_missing'
    Assert-Contract ($Definition.network_authorization -ceq 'structured_one_time_challenge') 'authorization_not_structured_challenge'
    Assert-Contract ($Definition.authorization_option -ceq 'consent') 'authorization_option_drift'
    Assert-Contract ($Definition.authorization_scope -ceq 'single_account_message_image_candidate_attempt') 'authorization_scope_drift'
    Assert-Contract (($Definition.authorization_ttl_seconds -as [int]) -eq 300) 'authorization_ttl_drift'
    Assert-Contract ($Definition.authorization_replay_protected -eq $true) 'replay_protection_missing'
    Assert-Contract ($Definition.authorization_consumed_before_network -eq $true) 'authorization_not_consumed_before_network'
    Assert-Contract (($Definition.network_attempts_per_authorization -as [int]) -eq 1) 'attempt_count_drift'
    Assert-Contract (($Definition.automatic_network_retries -as [int]) -eq 0 -and $Definition.network_method -ceq 'GET') 'automatic_retry_or_method_drift'
    Assert-Contract ($Definition.wechat_ui_automation -eq $false) 'ui_automation_overreach'
    Assert-Contract ($Definition.direct_url_source -ceq 'current_snapshot_descriptor_only') 'url_source_drift'
    Assert-Contract ($Definition.constructed_url_from_opaque_parameter -eq $false) 'opaque_parameter_url_construction_enabled'
    Assert-Contract ($Definition.allowed_destination -ceq 'novac2c.cdn.weixin.qq.com') 'destination_drift'
    Assert-Contract ($Definition.https_required -eq $true -and $Definition.redirects -eq $false) 'transport_boundary_drift'
    Assert-Contract ($Definition.ambient_proxy -eq $false -and $Definition.cookies -eq $false -and $Definition.external_dns_fallback -eq $false) 'ambient_credentials_enabled'
    Assert-Contract ($Definition.url_stored_in_consent -eq $false -and $Definition.descriptor_secrets_output -eq $false) 'descriptor_secret_persistence_enabled'
    Assert-Contract ($Definition.lower_quality_fallback -eq $false) 'lower_quality_fallback_enabled'
    Assert-Contract (($Definition.maximum_response_bytes -as [int64]) -eq 67108880) 'response_limit_drift'
    Assert-Contract ($Definition.source_original_quality_status -ceq 'unknown') 'original_quality_overclaim'
    Assert-Contract ($Definition.descriptor_expiry_default -ceq 'unknown_without_verified_request') 'descriptor_expiry_overclaim'
    Assert-Contract ($Definition.cleanup_failure_is_error -eq $true) 'cleanup_failure_hidden'
    Assert-Contract ($Definition.opaque_desktop_protocol_status -ceq 'unavailable_unverified_desktop_protocol') 'opaque_protocol_overclaim'

    $Bindings = @($Definition.authorization_bindings)
    foreach ($Expected in @('account_id', 'image_evidence_id', 'message_binding_sha256', 'generation_id', 'snapshot_manifest_sha256', 'local_quality_tier', 'candidate_descriptor_sha256', 'output_path_sha256')) {
        Assert-Contract ($Bindings -ccontains $Expected) "missing_binding_$Expected"
    }
    $Validation = @($Definition.response_validation)
    foreach ($Expected in @('mime', 'full_image_decode', 'descriptor_md5_or_size_plus_dimensions', 'message_binding', 'candidate_descriptor_fingerprint')) {
        Assert-Contract ($Validation -ccontains $Expected) "missing_validation_$Expected"
    }
    $Times = @($Definition.time_fields)
    foreach ($Expected in @('observed_at', 'retrieved_at', 'authorization_expires_at')) {
        Assert-Contract ($Times -ccontains $Expected) "missing_time_$Expected"
    }
}

try {
    if ($SelfTest) {
        $Definition = [pscustomobject]@{
            usage = 'v-local-cli recover-chat-image --output FILE [--account NAME] [--consent CHALLENGE] <image_evidence_id>'
            network_default = $false
            account_lock = $true
            account_lock_scope = 'entire_offline_preflight_or_authorized_attempt'
            force_supported = $false
            network_authorization = 'structured_one_time_challenge'
            authorization_option = 'consent'
            authorization_scope = 'single_account_message_image_candidate_attempt'
            authorization_ttl_seconds = 300
            authorization_replay_protected = $true
            authorization_consumed_before_network = $true
            authorization_bindings = @('account_id', 'image_evidence_id', 'message_binding_sha256', 'generation_id', 'snapshot_manifest_sha256', 'local_quality_tier', 'candidate_descriptor_sha256', 'output_path_sha256')
            network_attempts_per_authorization = 1
            automatic_network_retries = 0
            network_method = 'GET'
            wechat_ui_automation = $false
            direct_url_source = 'current_snapshot_descriptor_only'
            constructed_url_from_opaque_parameter = $false
            allowed_destination = 'novac2c.cdn.weixin.qq.com'
            https_required = $true
            redirects = $false
            ambient_proxy = $false
            cookies = $false
            external_dns_fallback = $false
            url_stored_in_consent = $false
            descriptor_secrets_output = $false
            lower_quality_fallback = $false
            maximum_response_bytes = 67108880
            response_validation = @('mime', 'full_image_decode', 'descriptor_md5_or_size_plus_dimensions', 'message_binding', 'candidate_descriptor_fingerprint')
            source_original_quality_status = 'unknown'
            descriptor_expiry_default = 'unknown_without_verified_request'
            time_fields = @('observed_at', 'retrieved_at', 'authorization_expires_at')
            cleanup_failure_is_error = $true
            opaque_desktop_protocol_status = 'unavailable_unverified_desktop_protocol'
        }
        Test-RecoveryDefinition $Definition
        [ordered]@{ self_test = 'passed'; network_access_performed = $false } | ConvertTo-Json -Compress
        exit 0
    }

    Assert-Contract (-not [string]::IsNullOrWhiteSpace($Cli)) 'cli_required'
    Assert-Contract ([System.IO.Path]::IsPathFullyQualified($Cli)) 'cli_path_not_absolute'
    Assert-Contract (Test-Path -LiteralPath $Cli -PathType Leaf) 'cli_not_found'
    $Raw = @(& $Cli schema recover-chat-image 2>&1)
    $ExitCode = $LASTEXITCODE
    Assert-Contract ($ExitCode -eq 0) 'schema_command_failed'
    $Envelope = (($Raw | ForEach-Object { $_.ToString() }) -join [Environment]::NewLine) | ConvertFrom-Json
    Assert-Contract ($Envelope.command_status -ceq 'succeeded' -and $Envelope.schema_version -eq 1) 'schema_envelope_invalid'
    Assert-Contract ($Envelope.data.command -ceq 'recover-chat-image' -and ($Envelope.data.contract_version -as [int]) -eq 1) 'schema_command_identity_invalid'
    Assert-Contract ($null -ne $Envelope.data.schema) 'schema_definition_missing'
    Test-RecoveryDefinition $Envelope.data.schema
    [ordered]@{
        contract = 'passed'
        command = 'recover-chat-image'
        network_access_performed = $false
        source_original_quality_status = 'unknown'
    } | ConvertTo-Json -Compress
    exit 0
}
catch {
    $Message = [string]$_.Exception.Message
    $Code = if ($Message.StartsWith('contract:', [System.StringComparison]::Ordinal)) { $Message.Substring('contract:'.Length) } else { 'unexpected_failure' }
    [ordered]@{ contract = 'failed'; failure_code = $Code; network_access_performed = $false } | ConvertTo-Json -Compress
    exit 1
}
