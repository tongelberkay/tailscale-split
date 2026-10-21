# Downloads the official Wintun driver DLL and checks that it is signed by
# WireGuard LLC before copying it next to tailsplit.exe.
param([string]$Destination = 'dist')
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$version = '0.14.1'
$arch = @{ 'AMD64' = 'amd64'; 'ARM64' = 'arm64'; 'x86' = 'x86' }[$env:PROCESSOR_ARCHITECTURE]
$tmp = Join-Path ([IO.Path]::GetTempPath()) ('wintun-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  $zip = Join-Path $tmp 'wintun.zip'
  Invoke-WebRequest -Uri "https://www.wintun.net/builds/wintun-$version.zip" -OutFile $zip -UseBasicParsing
  Write-Host ('wintun-{0}.zip SHA-256: {1}' -f $version, (Get-FileHash $zip -Algorithm SHA256).Hash)
  Expand-Archive -LiteralPath $zip -DestinationPath $tmp
  $dll = Join-Path $tmp "wintun\bin\$arch\wintun.dll"
  $sig = Get-AuthenticodeSignature -LiteralPath $dll
  if ($sig.Status -ne 'Valid' -or $sig.SignerCertificate.Subject -notmatch 'O=WireGuard LLC') {
    throw "wintun.dll signature check failed: $($sig.Status) $($sig.SignerCertificate.Subject)"
  }
  New-Item -ItemType Directory -Force -Path $Destination | Out-Null
  Copy-Item -LiteralPath $dll -Destination $Destination -Force
  Write-Host "wintun.dll ($arch, signed by WireGuard LLC) copied to $Destination"
} finally {
  Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
}
