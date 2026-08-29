<#
.SYNOPSIS
Creates an empty, ACL-restricted directory for one WXGF visual-review session.

.DESCRIPTION
The directory is intended for V_LOCAL_TEST_WXGF_REVIEW_ROOT. It contains private
decoded images and user-provided reference screenshots only until the companion
acceptance script records the review and removes the temporary artifacts.

.NOTES
PowerShell 7 and Windows are required. This script does not read WeChat data.
#>
[CmdletBinding()]
param(
    [string]$RootBase,
    [switch]$ShowPaths,
    [switch]$SelfTest
)

Set-StrictMode -Version 3.0

function Stop-Session {
    param([Parameter(Mandatory = $true)][string]$Code)
    throw [System.InvalidOperationException]::new("wxgf-review-session:$Code")
}

function Assert-LocalAbsolutePath {
    param([Parameter(Mandatory = $true)][string]$Path)
    if (-not [System.IO.Path]::IsPathFullyQualified($Path) -or $Path.StartsWith('\\')) {
        Stop-Session 'root_not_local_absolute'
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
        if (-not (Test-Path -LiteralPath $Current)) {
            continue
        }
        $Item = Get-Item -LiteralPath $Current -Force -ErrorAction Stop
        if (($Item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
            Stop-Session 'root_contains_reparse_point'
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

function Assert-PrivateDirectoryAcl {
    param([Parameter(Mandatory = $true)][string]$Path)
    $CurrentUser = [System.Security.Principal.WindowsIdentity]::GetCurrent().User
    $Trusted = @(
        $CurrentUser.Value,
        'S-1-5-18',
        'S-1-5-32-544'
    )
    $Security = Get-Acl -LiteralPath $Path -ErrorAction Stop
    if (-not $Security.AreAccessRulesProtected) {
        Stop-Session 'root_base_acl_inherited'
    }
    try {
        $Owner = ([System.Security.Principal.NTAccount]$Security.Owner).Translate(
            [System.Security.Principal.SecurityIdentifier]
        ).Value
    }
    catch {
        Stop-Session 'root_base_owner_invalid'
    }
    if ($Trusted -cnotcontains $Owner) {
        Stop-Session 'root_base_owner_invalid'
    }
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
        if ($Trusted -cnotcontains $Sid) {
            Stop-Session 'root_base_acl_untrusted_allow'
        }
        if ($Sid -ceq $CurrentUser.Value) {
            $CurrentUserAllowed = $true
        }
    }
    if (-not $CurrentUserAllowed) {
        Stop-Session 'root_base_acl_current_user_missing'
    }
}

if ($SelfTest) {
    [ordered]@{
        self_test = 'passed'
        private_material_lifetime = 'until_explicit_review_or_cleanup'
        reads_wechat_data = $false
        opens_wechat_ui = $false
    } | ConvertTo-Json -Compress
    exit 0
}

try {
    if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
        Stop-Session 'windows_required'
    }
    if ($PSVersionTable.PSVersion.Major -lt 7) {
        Stop-Session 'powershell_7_required'
    }
    if ([string]::IsNullOrWhiteSpace($RootBase)) {
        if ([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
            Stop-Session 'local_app_data_unavailable'
        }
        $RootBase = Join-Path $env:LOCALAPPDATA 'v-local\wxgf-review-sessions'
    }
    Assert-LocalAbsolutePath $RootBase
    $RootBaseCreated = $false
    if (-not (Test-Path -LiteralPath $RootBase)) {
        [void](New-Item -ItemType Directory -Path $RootBase -ErrorAction Stop)
        $RootBaseCreated = $true
    }
    Assert-NoReparsePoint $RootBase
    $RootBaseItem = Get-Item -LiteralPath $RootBase -Force -ErrorAction Stop
    if (-not $RootBaseItem.PSIsContainer) {
        Stop-Session 'root_base_invalid'
    }
    if ($RootBaseCreated) {
        Set-PrivateDirectoryAcl $RootBase
    }
    Assert-PrivateDirectoryAcl $RootBase
    $RunId = [DateTime]::UtcNow.ToString('yyyyMMdd-HHmmss') + '-' + [Guid]::NewGuid().ToString('N').Substring(0, 8)
    $ReviewRoot = Join-Path $RootBase $RunId
    if (Test-Path -LiteralPath $ReviewRoot) {
        Stop-Session 'review_root_exists'
    }
    [void](New-Item -ItemType Directory -Path $ReviewRoot -ErrorAction Stop)
    Assert-NoReparsePoint $ReviewRoot
    Set-PrivateDirectoryAcl $ReviewRoot
    $Result = [ordered]@{
        status = 'prepared'
        review_root_included = [bool]$ShowPaths
        temporary_images_present = $false
        next = 'set V_LOCAL_TEST_WXGF_REVIEW_ROOT only for the opt-in qualification test'
    }
    if ($ShowPaths) {
        $Result.review_root = [System.IO.Path]::GetFullPath($ReviewRoot)
    }
    $Result | ConvertTo-Json -Compress
    exit 0
}
catch {
    $Code = if ([string]$_.Exception.Message -like 'wxgf-review-session:*') {
        ([string]$_.Exception.Message).Substring('wxgf-review-session:'.Length)
    }
    else {
        'unexpected_failure'
    }
    [ordered]@{ status = 'failed'; failure_code = $Code; review_root_included = $false } |
        ConvertTo-Json -Compress
    exit 1
}
