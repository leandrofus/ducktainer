# Installs the MSI for real and checks the service and the sign-in with
# Windows accounts (used by .github/workflows/windows.yml; needs an elevated
# PowerShell 7 on a throwaway machine: it creates local users and groups).
#
#   ./test-msi.ps1 -Msi ducktainer-0.0.1.msi [-Upgrade ducktainer-0.0.2.msi -UpgradeVersion v0.0.2] [-Uninstall]
#   ./test-msi.ps1 -Msi ducktainer-0.0.1.msi -Wsl squeak-wsl   # the service reaches WSL through the relay
param(
  [Parameter(Mandatory)][string]$Msi,
  [string]$Upgrade,
  [string]$UpgradeVersion,
  [string]$Wsl,
  [switch]$Uninstall
)
$ErrorActionPreference = 'Stop'
$Msi = (Resolve-Path $Msi).Path
if ($Upgrade) { $Upgrade = (Resolve-Path $Upgrade).Path }
$base = 'http://127.0.0.1:9900'
$exe = "$env:ProgramFiles\ducktainer\ducktainer.exe"
$data = "$env:ProgramData\ducktainer"
$pw = 'Dkt-Passw0rd!2026'

function Check($ok, $what) {
  if (-not $ok) {
    if (Test-Path "$data\ducktainer.log") { Get-Content "$data\ducktainer.log" -Tail 60 }
    throw "FAILED: $what"
  }
  Write-Host "ok  $what"
}

function Msiexec([string[]]$msiArgs, $log) {
  $p = Start-Process msiexec.exe -ArgumentList ($msiArgs + '/qn', '/l*v', "`"$log`"") -Wait -PassThru
  if ($p.ExitCode -ne 0) {
    Get-Content $log -Tail 80
    throw "msiexec $($msiArgs -join ' '): exit $($p.ExitCode)"
  }
}

function WaitUp {
  for ($i = 0; $i -lt 60; $i++) {
    try { return Invoke-RestMethod "$base/api/auth" } catch { Start-Sleep -Seconds 1 }
  }
  Check $false 'the service answers on 127.0.0.1:9900'
}

function Login($user, $pass) {
  $r = Invoke-WebRequest "$base/api/login" -Method Post -ContentType 'application/json' `
    -Body (@{ username = $user; password = $pass } | ConvertTo-Json) -SessionVariable s -SkipHttpErrorCheck
  $body = $null
  try { $body = $r.Content | ConvertFrom-Json } catch {}
  return [pscustomobject]@{ Status = [int]$r.StatusCode; Body = $body; Session = $s }
}

function User($name, $group) {
  if (-not (Get-LocalUser $name -ErrorAction SilentlyContinue)) {
    New-LocalUser $name -Password (ConvertTo-SecureString $pw -AsPlainText -Force) -PasswordNeverExpires | Out-Null
  }
  if ($group) {
    if (-not (Get-LocalGroup $group -ErrorAction SilentlyContinue)) { New-LocalGroup $group | Out-Null }
    if (-not (Get-LocalGroupMember $group -Member $name -ErrorAction SilentlyContinue)) { Add-LocalGroupMember $group -Member $name }
  }
}

# ---- install ----
Msiexec '/i', "`"$Msi`"" "$Msi.log"
$svc = Get-Service ducktainer
Check ($svc.Status -eq 'Running') "service installed and running ($($svc.Status))"
Check ((Get-CimInstance Win32_Service -Filter "Name='ducktainer'").StartName -eq 'LocalSystem') 'runs as LocalSystem'
Check (Test-Path $exe) "binary in $exe"
if (Test-Path "$env:ProgramFiles\ducktainer\agent") {
  Check ((Test-Path "$env:ProgramFiles\ducktainer\agent\ducktainer-agent-linux-amd64") -and (Test-Path "$env:ProgramFiles\ducktainer\agent\ducktainer-agent-linux-arm64")) 'the static Linux agents are installed'
}
Check (([Environment]::GetEnvironmentVariable('PATH', 'Machine') -split ';') -contains "$env:ProgramFiles\ducktainer\") 'install folder on the machine PATH'
$info = WaitUp
Check ($info.os -eq 'windows' -and $info.pam -eq $true) "sign-in with Windows accounts on by default ($($info | ConvertTo-Json -Compress))"

# ---- data folder: only SYSTEM and Administrators ----
$acl = Get-Acl $data
$who = $acl.Access | ForEach-Object { $_.IdentityReference.Translate([Security.Principal.SecurityIdentifier]).Value }
Check ($acl.AreAccessRulesProtected) 'data folder does not inherit from ProgramData'
Check (-not ($who | Where-Object { $_ -notin 'S-1-5-18', 'S-1-5-32-544' })) "data folder only for SYSTEM and Administrators ($($who -join ', '))"
Check (Test-Path "$data\config.json") 'config in ProgramData'

# ---- sign-in ----
User 'dkt-admin' 'ducktainer-admins'
User 'dkt-viewer' 'ducktainer-viewers'
User 'dkt-nobody'
$r = Login 'dkt-admin' $pw
Check ($r.Status -eq 200 -and $r.Body.role -eq 'admin') "dkt-admin signs in as admin ($($r.Status))"
$admin = $r.Session
$me = Invoke-RestMethod "$base/api/me" -WebSession $admin
Check ($me.method -eq 'pam' -and $me.user -eq 'dkt-admin') "session of dkt-admin ($($me | ConvertTo-Json -Compress))"
$r = Login "$env:COMPUTERNAME\DKT-ADMIN" $pw
Check ($r.Status -eq 200 -and $r.Body.role -eq 'admin') "COMPUTER\user, any case ($($r.Status))"
$r = Login '.\dkt-viewer' $pw
Check ($r.Status -eq 200 -and $r.Body.role -eq 'viewer') ".\dkt-viewer signs in read-only ($($r.Status))"
$r = Login 'dkt-viewer' 'wrong-password'
Check ($r.Status -eq 401) "wrong password refused ($($r.Status))"
$r = Login 'dkt-nobody' $pw
Check ($r.Status -eq 403) "user outside the role groups refused ($($r.Status) $($r.Body.error))"
$acc = Invoke-RestMethod "$base/api/settings/access" -WebSession $admin
$admins = ($acc.roles | Where-Object role -eq 'admin').groups | Where-Object name -eq 'ducktainer-admins'
Check ($admins.exists -and ($admins.members -match 'dkt-admin')) "access page lists the members of ducktainer-admins ($($admins | ConvertTo-Json -Compress))"

# ---- CLI as an elevated administrator: uses the service's data ----
$out = $pw | & $exe auth test dkt-admin 2>&1 | Out-String
Check ($LASTEXITCODE -eq 0 -and $out -match 'admin') "ducktainer auth test: $out"
$out = & $exe ui 2>&1 | Out-String
Check ($LASTEXITCODE -eq 0 -and $out -match '9900') "ducktainer ui opens the running service: $out"

if ($Wsl) {
  # the service (LocalSystem) can't use WSL itself: it finds the relay that
  # `engine install --mode wsl` started for this administrator
  $ep = $null
  for ($i = 0; $i -lt 90 -and -not $ep; $i++) {
    $ep = (Invoke-RestMethod "$base/api/endpoints" -WebSession $admin) | Where-Object { $_.url -eq 'wsl://ducktainer' }
    if (-not $ep) { Start-Sleep -Seconds 1 }
  }
  Check $ep 'the service added the WSL endpoint served by the relay'
  $names = (Invoke-RestMethod "$base/api/e/$($ep.name)/containers" -WebSession $admin) | ForEach-Object { $_.Names } | Out-String
  Check ($names -match $Wsl) "the service sees $Wsl through the relay"
}

if ($Upgrade) {
  $token = (Get-FileHash "$data\config.json").Hash
  Msiexec '/i', "`"$Upgrade`"" "$Upgrade.log"
  $info = WaitUp
  Check ($info.version -eq $UpgradeVersion) "upgraded to $UpgradeVersion ($($info.version))"
  Check ((Get-Service ducktainer).Status -eq 'Running') 'service running after the upgrade'
  $r = Login 'dkt-admin' $pw
  Check ($r.Status -eq 200) "sign-in after the upgrade ($($r.Status))"
  $installed = Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*' | Where-Object DisplayName -eq 'ducktainer'
  Check (@($installed).Count -eq 1) "a single ducktainer in Installed apps ($(@($installed).DisplayVersion -join ', '))"
  $Msi = $Upgrade
}

if ($Uninstall) {
  Msiexec '/x', "`"$Msi`"" "$Msi.uninstall.log"
  Check (-not (Get-Service ducktainer -ErrorAction SilentlyContinue)) 'service removed'
  Check (-not (Test-Path $exe)) 'binary removed'
  Check (Test-Path "$data\config.json") 'data kept'
}
