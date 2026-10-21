# Builds dist\tailsplit.exe (one file) and fetches wintun.dll beside it for TUN mode.
# Needs Go 1.25+ (https://go.dev/dl/). Nothing is installed system-wide.
$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath $PSScriptRoot

$tags = @(
  'ts_omit_aws','ts_omit_bird','ts_omit_tap','ts_omit_kube','ts_omit_completion','ts_omit_ssh',
  'ts_omit_wakeonlan','ts_omit_capture','ts_omit_relayserver','ts_omit_taildrop','ts_omit_tpm',
  'ts_omit_debugeventbus','ts_omit_webclient','ts_omit_drive','ts_omit_clientupdate','ts_omit_netlog',
  'ts_omit_logtail','ts_omit_advertiseexitnode','ts_omit_advertiseroutes','ts_omit_portlist',
  'ts_omit_posture','ts_omit_appconnectors','ts_omit_tailnetlock','ts_omit_doctor','ts_omit_syspolicy',
  'ts_omit_c2n','ts_omit_qrcodes','ts_omit_flashappliance','ts_omit_conn25','ts_omit_dbus',
  'ts_omit_synology','ts_omit_networkmanager','ts_omit_resolved','ts_omit_iptables',
  'ts_omit_linuxdnsfight','ts_omit_sdnotify','ts_omit_androidbin','ts_omit_androiddns',
  'ts_omit_identityfederation','ts_omit_oauthkey','ts_omit_cloud','ts_omit_serviceclientprefs',
  'ts_omit_remoteconfig'
) -join ','

go test ./...
if ($LASTEXITCODE -ne 0) { throw 'Tests failed.' }

New-Item -ItemType Directory -Force -Path dist | Out-Null
$env:CGO_ENABLED = '0'
$version = if ($env:TAILSPLIT_VERSION) { $env:TAILSPLIT_VERSION } else { 'dev' }
go build -trimpath -tags $tags -ldflags "-s -w -H windowsgui -X main.version=$version" -o dist\tailsplit.exe .
if ($LASTEXITCODE -ne 0) { throw 'Build failed.' }

# Only TUN mode needs wintun.dll; proxy mode (the default) runs without it.
if (!(Test-Path -LiteralPath dist\wintun.dll)) {
  try { & .\fetch-wintun.ps1 -Destination dist } catch { Write-Warning "wintun.dll not fetched (only needed for TUN mode): $_" }
}
$size = [math]::Round((Get-Item dist\tailsplit.exe).Length / 1MB, 1)
Write-Host "Built dist\tailsplit.exe ($size MB). For TUN mode keep wintun.dll in the same folder."
