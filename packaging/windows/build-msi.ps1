# Builds the MSI with the WiX Toolset (installed as a .NET tool if missing).
#
#   ./build-msi.ps1 -Version 1.5.0 -Exe dist/ducktainer.exe -Out dist/ducktainer-1.5.0-x64.msi [-AgentDir dist/agent]
param(
  [Parameter(Mandatory)][string]$Version,
  [Parameter(Mandatory)][string]$Exe,
  [Parameter(Mandatory)][string]$Out,
  # folder with ducktainer-agent-linux-amd64 and -arm64: the static agents the server hands to Linux nodes
  [string]$AgentDir
)
$ErrorActionPreference = 'Stop'
$wixVersion = '5.0.2'
if (-not (Get-Command wix -ErrorAction SilentlyContinue)) {
  dotnet tool install --global wix --version $wixVersion
  if ($LASTEXITCODE) { throw 'could not install wix' }
  $env:PATH += ";$env:USERPROFILE\.dotnet\tools"
}
$defs = @('-d', "Version=$Version", '-d', "Exe=$((Resolve-Path $Exe).Path)")
if ($AgentDir) { $defs += @('-d', "AgentDir=$((Resolve-Path $AgentDir).Path)") }
wix build "$PSScriptRoot\ducktainer.wxs" -arch x64 @defs -o $Out
if ($LASTEXITCODE) { throw "wix build: exit $LASTEXITCODE" }
Get-Item $Out | Format-Table Name, Length
