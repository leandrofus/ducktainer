package server

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// psQuote quotes v for a PowerShell single-quoted string.
func psQuote(v string) string { return "'" + strings.ReplaceAll(v, "'", "''") + "'" }

// pinSource is the certificate pin the Windows installer uses: Windows
// PowerShell 5.1 cannot pin a certificate with Invoke-WebRequest, so it
// downloads with an HttpClient whose validation callback compares the sha256
// of the server certificate. Plain C# 5, which is what Add-Type compiles in 5.1.
const pinSource = `using System; using System.Net.Http; using System.Net.Security; using System.Security.Cryptography; using System.Security.Cryptography.X509Certificates; public static class DuckPin { public static string Fp = ""; public static Func<HttpRequestMessage, X509Certificate2, X509Chain, SslPolicyErrors, bool> Callback = Check; static bool Check(HttpRequestMessage m, X509Certificate2 c, X509Chain h, SslPolicyErrors e) { using (SHA256 s = SHA256.Create()) { return BitConverter.ToString(s.ComputeHash(c.RawData)).Replace("-", "").ToLowerInvariant() == Fp; } } }`

// psRefs lists the assemblies Add-Type needs to compile pinSource: Windows
// PowerShell (.NET Framework) finds the rest in its defaults, PowerShell 7
// (.NET) keeps them in separate assemblies.
const psRefs = `$(if ($PSVersionTable.PSEdition -eq 'Core') { 'System.Net.Http','System.Net.Primitives','System.Security.Cryptography','System.Security.Cryptography.X509Certificates' } else { 'System.Net.Http' })`

// psHTTPClient is the PowerShell that builds $w, an HttpClient that carries
// the token and pins the server's certificate (when it has one).
func psHTTPClient(token, fp string) string {
	var b strings.Builder
	b.WriteString("[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12; ")
	b.WriteString("if (-not ('DuckPin' -as [type])) { Add-Type -ReferencedAssemblies " + psRefs + " -TypeDefinition " + psQuote(pinSource) + " }; ")
	b.WriteString("$c = New-Object System.Net.Http.HttpClientHandler; ")
	if fp != "" {
		b.WriteString("[DuckPin]::Fp = " + psQuote(fp) + "; $c.ServerCertificateCustomValidationCallback = [DuckPin]::Callback; ")
	}
	b.WriteString("$w = New-Object System.Net.Http.HttpClient($c); $w.Timeout = [TimeSpan]::FromMinutes(5); ")
	b.WriteString("$w.DefaultRequestHeaders.Add('Authorization', " + psQuote("Bearer "+token) + ")")
	return b.String()
}

// psInstallCommand is what an administrator pastes in an elevated PowerShell
// on the node: it fetches the installer over the pinned connection and runs it.
func psInstallCommand(server, token, fp string) string {
	script := server + "/api/agent/install.ps1?server=" + url.QueryEscape(server)
	return "$ErrorActionPreference = 'Stop'; " + psHTTPClient(token, fp) +
		"; iex ($w.GetStringAsync(" + psQuote(script) + ").GetAwaiter().GetResult())"
}

// psInstallScript is the installer `install.ps1` returns. Like the Linux one it
// checks everything before touching the node and installs nothing if a check
// fails.
const psInstallScript = `# ducktainer agent installer (Windows)
$Server = @@SERVER@@
$Name = @@NAME@@
$Fingerprint = @@FP@@
$Token = @@TOKEN@@
$Archs = @@ARCHS@@
$ErrorActionPreference = 'Stop'
$tmp = $null
try {
	Write-Host "Installing ducktainer-agent as $Name (server: $Server)"
	$id = [Security.Principal.WindowsIdentity]::GetCurrent()
	if (-not (New-Object Security.Principal.WindowsPrincipal($id)).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
		throw 'run it from an elevated PowerShell (Run as administrator): the agent is installed as a Windows service'
	}

	# Everything is checked before anything on this node is touched.
	$arch = $env:PROCESSOR_ARCHITEW6432
	if (-not $arch) { $arch = $env:PROCESSOR_ARCHITECTURE }
	switch ($arch) { 'AMD64' { $arch = 'amd64' } 'ARM64' { $arch = 'arm64' } default { $arch = "$arch".ToLower() } }
	if ($Archs -notcontains $arch) {
		throw "this server provides Windows agent binaries for: $($Archs -join ', '); this node is windows/$arch"
	}

	[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
	if (-not ('DuckPin' -as [type])) { Add-Type -ReferencedAssemblies @@REFS@@ -TypeDefinition @@PIN@@ }
	$handler = New-Object System.Net.Http.HttpClientHandler
	if ($Fingerprint) { [DuckPin]::Fp = $Fingerprint; $handler.ServerCertificateCustomValidationCallback = [DuckPin]::Callback }
	$web = New-Object System.Net.Http.HttpClient($handler)
	$web.Timeout = [TimeSpan]::FromMinutes(5)
	$web.DefaultRequestHeaders.Add('Authorization', "Bearer $Token")

	$tmp = Join-Path $env:TEMP ("ducktainer-agent-" + [guid]::NewGuid().ToString('N') + '.exe')
	try {
		$resp = $web.GetAsync("$Server/api/agent/binary?os=windows&arch=$arch").GetAwaiter().GetResult()
	} catch {
		$why = $_.Exception.GetBaseException().Message
		if ($why -match 'certificate|validation procedure') { throw 'the server certificate does not match the pinned one: this is not the server the command was made for' }
		if ($why -match 'actively refused|refused') { throw "cannot connect to $Server (connection refused). From this node 'localhost' is the node itself: use an address of the ducktainer server that this node can reach" }
		if ($why -match 'forcibly closed|was closed|reset|unexpected EOF') { throw "$Server closed the connection. ducktainer drops addresses that are not in its allowlist: on the server run 'ducktainer allow add <IP of this node>' and restart it, or start it with --agent-listen :9901 (agents only, token required) and use that port" }
		if ($why -match 'timed out|timeout|time out') { throw "timed out connecting to ${Server}: a firewall may be dropping the connection" }
		if ($why -match 'name or service|No such host|could not be resolved') { throw "cannot resolve the name in $Server" }
		throw "could not download the agent from $Server ($why)"
	}
	if ($resp.StatusCode -eq 401) { throw 'the server refused the token (is this host still registered in ducktainer?)' }
	if (-not $resp.IsSuccessStatusCode) { throw "the server answered $([int]$resp.StatusCode) $($resp.ReasonPhrase)" }
	$stream = $resp.Content.ReadAsStreamAsync().GetAwaiter().GetResult()
	$file = [IO.File]::Create($tmp)
	try { $stream.CopyTo($file) } finally { $file.Dispose(); $stream.Dispose() }

	# a binary that does not run here must not replace anything
	try { $out = (& $tmp version 2>&1 | Out-String).Trim() } catch { $out = "$_" }
	if ($LASTEXITCODE -ne 0 -or $out -notmatch '^ducktainer ') { throw "the agent binary does not run on this node: $out" }
	Write-Host "agent binary OK ($out)"

	$env:DUCKTAINER_AGENT_TOKEN = $Token
	$flags = @('agent', '--install-service', '--server', $Server, '--name', $Name)
	if ($Fingerprint) { $flags += @('--fingerprint', $Fingerprint) }
	& $tmp @flags
	if ($LASTEXITCODE -ne 0) { throw 'installing the service failed' }
} catch {
	Write-Host "error: $($_.Exception.Message)" -ForegroundColor Red
	Write-Host 'The agent was not installed.' -ForegroundColor Red
} finally {
	Remove-Item Env:\DUCKTAINER_AGENT_TOKEN -ErrorAction SilentlyContinue
	if ($tmp -and (Test-Path $tmp)) { Remove-Item $tmp -Force -ErrorAction SilentlyContinue }
}
`

// serveAgentInstallPS returns the installer run by the PowerShell command of
// the UI on a Windows node; the token in the request decides which host it enrolls.
func (s *Server) serveAgentInstallPS(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.agentEndpoint(bearer(r))
	if !ok {
		http.Error(w, "invalid or missing token", http.StatusUnauthorized)
		return
	}
	scheme := "http"
	if s.Secure || r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	serverURL := fmt.Sprintf("%s://%s", scheme, r.Host)
	if custom := strings.TrimRight(r.URL.Query().Get("server"), "/"); validServerURL(custom) {
		serverURL = custom
	}
	fp, _ := s.agentPins()
	archs := make([]string, 0, 2)
	for _, a := range agentArchsFor("windows") {
		archs = append(archs, psQuote(a))
	}
	list := "@()"
	if len(archs) > 0 {
		list = "@(" + strings.Join(archs, ", ") + ")"
	}
	script := strings.NewReplacer(
		"@@SERVER@@", psQuote(serverURL),
		"@@NAME@@", psQuote(ep.Name),
		"@@FP@@", psQuote(fp),
		"@@TOKEN@@", psQuote(ep.Token),
		"@@ARCHS@@", list,
		"@@PIN@@", psQuote(pinSource),
		"@@REFS@@", psRefs,
	).Replace(psInstallScript)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(script))
}
