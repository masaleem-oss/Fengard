# points this pc at local fengard and trusts its cert undo with test-mode-off.ps1
#Requires -RunAsAdministrator
$root = Split-Path $PSScriptRoot -Parent
$ca = Join-Path $root "dev\data\ca.crt"
if (-not (Test-Path $ca)) { throw "ca.crt not found - start fengardd once first (dev\run.ps1)." }

# windows pops a confirm dialog here
Import-Certificate -FilePath $ca -CertStoreLocation Cert:\CurrentUser\Root | Out-Null
Write-Host "Installed certificate: $((Get-PfxCertificate $ca).Subject)"

Get-NetAdapter | Where-Object Status -eq Up | ForEach-Object {
    Set-DnsClientServerAddress -InterfaceIndex $_.InterfaceIndex -ServerAddresses "127.0.0.1", "::1"
    Write-Host "DNS -> Fengard on $($_.Name)"
}
Clear-DnsClientCache
Write-Host "`nTest mode ON. Restart your browser so it drops cached connections."
Write-Host "If fengardd stops, internet lookups stop too - run test-mode-off.ps1 to restore."
