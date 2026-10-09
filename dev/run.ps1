# runs fengardd locally for testing data goes in dev\data
# dev\leases.txt is a fake dhcp list so this pc shows up as a device
$root = Split-Path $PSScriptRoot -Parent
Set-Location $root
& "$root\bin\fengardd.exe" `
    -dns "127.0.0.1:53,[::1]:53" `
    -http 127.0.0.1:80 `
    -https 127.0.0.1:443 `
    -block-ip 127.0.0.1 `
    -data dev\data `
    -leases dev\leases.txt
