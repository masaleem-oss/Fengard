# puts dns back to dhcp and removes the test cert
#Requires -RunAsAdministrator
Get-NetAdapter | Where-Object Status -eq Up | ForEach-Object {
    Set-DnsClientServerAddress -InterfaceIndex $_.InterfaceIndex -ResetServerAddresses
    Write-Host "DNS -> automatic on $($_.Name)"
}
Clear-DnsClientCache

Get-ChildItem Cert:\CurrentUser\Root |
    Where-Object Subject -like "CN=Fengard Local CA*" |
    ForEach-Object { Remove-Item $_.PSPath; Write-Host "Removed certificate: $($_.Subject)" }

Write-Host "`nTest mode OFF."
