# fengard setup for windows Install Fengard.cmd runs this and no args shows a menu
# actions are router computer remove-router remove-computer
# .\fengard-setup.ps1 -Action router -Router 192.168.8.1
# -AdGuardOff lets the router install turn AdGuard Home off when it holds port 53
param(
    [ValidateSet('', 'router', 'computer', 'remove-router', 'remove-computer')]
    [string]$Action = '',
    [string]$Router = '',
    [int]$SshPort = 22,
    [switch]$Purge,
    [switch]$AdGuardOff,
    [switch]$NoPause
)

$ErrorActionPreference = 'Stop'
$Kit = Split-Path $PSScriptRoot -Parent
$Bundle = Join-Path $PSScriptRoot 'linux-bundle.tar.gz'
$TaskName = 'Fengard'
$InstallDir = Join-Path $env:ProgramFiles 'Fengard'
$DataDir = Join-Path $env:ProgramData 'Fengard'

function Say([string]$msg) { Write-Host "== $msg" -ForegroundColor Cyan }
function Note([string]$msg) { Write-Host "   $msg" }
function Warn([string]$msg) { Write-Host "WARNING: $msg" -ForegroundColor Yellow }
function Fail([string]$msg) { throw $msg }

function Test-Admin {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    return (New-Object Security.Principal.WindowsPrincipal($id)).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Get-DefaultRoute {
    Get-NetRoute -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue |
        Where-Object { $_.NextHop -ne '0.0.0.0' } |
        Sort-Object RouteMetric |
        Select-Object -First 1
}

function Test-TcpPort([string]$hostName, [int]$port) {
    $c = New-Object Net.Sockets.TcpClient
    try {
        $ar = $c.BeginConnect($hostName, $port, $null, $null)
        if (-not $ar.AsyncWaitHandle.WaitOne(4000)) { return $false }
        $c.EndConnect($ar)
        return $true
    } catch { return $false } finally { $c.Close() }
}

function Get-Ssh {
    $cmd = Get-Command ssh.exe -ErrorAction SilentlyContinue
    if (-not $cmd) {
        Fail ("Windows' SSH client isn't installed. Open Settings > System > Optional features, add 'OpenSSH Client', then run this again. " +
            "Or, as Administrator: Add-WindowsCapability -Online -Name OpenSSH.Client~~~~0.0.1.0")
    }
    return $cmd.Source
}

function Get-RouterAddress {
    if ($Router) { return $Router }
    $gw = (Get-DefaultRoute).NextHop
    if ($gw) {
        $answer = Read-Host "Router address [$gw]"
        if ($answer) { return $answer.Trim() }
        return $gw
    }
    $answer = Read-Host 'Router address (for example 192.168.1.1)'
    if (-not $answer) { Fail 'No router address given.' }
    return $answer.Trim()
}

# goes through cmd.exe since ps 5.1 pipes mangle the upload
function Invoke-Ssh([string]$address, [string]$remote, [string]$stdinFile = '') {
    $ssh = Get-Ssh
    $sshDir = Join-Path $HOME '.ssh'
    if (-not (Test-Path $sshDir)) { New-Item -ItemType Directory -Path $sshDir | Out-Null }
    $opts = "-p $SshPort -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new " +
        '-o UserKnownHostsFile=~/.ssh/fengard_known_hosts -o HostKeyAlgorithms=+ssh-rsa'
    $line = "`"$ssh`" $opts root@$address `"$remote`""
    if ($stdinFile) { $line += " < `"$stdinFile`"" }
    $p = New-Object Diagnostics.Process
    $p.StartInfo.FileName = Join-Path $env:SystemRoot 'System32\cmd.exe'
    $p.StartInfo.Arguments = "/d /s /c `"$line`""
    $p.StartInfo.UseShellExecute = $false
    [void]$p.Start()
    $p.WaitForExit()
    return $p.ExitCode
}

function Invoke-SshWithRetry([string]$address, [string]$remote, [string]$stdinFile = '') {
    $code = Invoke-Ssh $address $remote $stdinFile
    if ($code -ne 255) { return $code }
    Write-Host ''
    Write-Host 'Could not log in to the router. Check the password (on most routers it is the admin page password).'
    Write-Host 'If the router was reset or replaced, it has a new identity and SSH refuses it until the old one is forgotten.'
    $again = Read-Host 'Forget the router''s old identity and try again? [y/N]'
    if ($again -notmatch '^[yY]') { return $code }
    $kh = Join-Path $HOME '.ssh\fengard_known_hosts'
    if (Test-Path $kh) {
        $hostKey = $address
        if ($SshPort -ne 22) { $hostKey = "[$address]:$SshPort" }
        & ssh-keygen.exe -R $hostKey -f $kh 2>$null | Out-Null
    }
    return Invoke-Ssh $address $remote $stdinFile
}

function Install-Router {
    if (-not (Test-Path $Bundle)) {
        Fail "The Fengard programs aren't next to this script ($Bundle is missing). Use the downloaded Fengard kit, or build it with: go run ./tools/release -version dev"
    }
    $address = Get-RouterAddress
    if (-not (Test-TcpPort $address $SshPort)) {
        Fail ("Can't reach SSH on $address (port $SshPort). Check the address, and that SSH is turned on in the router's settings " +
            '(OpenWrt: System > Administration > SSH Access; GL.iNet: on by default).')
    }
    Say "Installing Fengard on the router at $address"
    Note 'Enter the router''s root password when asked (on most routers it is the admin page password).'
    Note 'The upload carries Fengard for every router CPU; only the one this router needs is unpacked.'
    # keep in sync with install.sh
    $agh = ''
    if ($AdGuardOff) { $agh = 'ADGUARD=off ' }
    while ($true) {
        $remote = 'rm -rf /tmp/fengard-install; mkdir -p /tmp/fengard-install && cd /tmp/fengard-install || exit 1; ' +
            'if [ ! -f /etc/openwrt_release ]; then echo; echo This router does not run OpenWrt-based firmware, which Fengard needs.; cat >/dev/null; exit 3; fi; ' +
            'a=$(. /etc/openwrt_release; echo $DISTRIB_ARCH); case $a in aarch64*) t=arm64;; arm*) t=arm;; x86_64*) t=amd64;; i?86*) t=386;; ' +
            'mips64el*) t=mips64le;; mips64*) t=mips64;; mipsel*) t=mipsle;; mips*) t=mips;; riscv64*) t=riscv64;; loongarch64*) t=loong64;; ' +
            '*) echo Unsupported router CPU: $a; cat >/dev/null; exit 4;; esac; ' +
            'tar -xzf - router-install.sh router-uninstall.sh bin/linux-$t/fengardd || { echo The upload failed: not enough free memory on the router?; exit 5; }; ' +
            $agh + 'sh router-install.sh'
        $code = Invoke-SshWithRetry $address $remote $Bundle
        Write-Host ''
        # 7 means adguard home has port 53, ask here since the router has no terminal
        if ($code -ne 7 -or $agh) { break }
        $answer = Read-Host 'Turn AdGuard Home off so Fengard can take over port 53? Removing Fengard turns it back on. [y/N]'
        if ($answer -notmatch '^[yY]') { break }
        $agh = 'ADGUARD=off '
    }
    switch ($code) {
        0 { Say 'Done. Open the dashboard address shown above and create the admin account.' }
        255 { Fail 'Could not connect to the router over SSH.' }
        7 { Fail 'AdGuard Home is still using port 53, so nothing was changed.' }
        default { Fail "The router install stopped (code $code); see the messages above. The router's own DNS setup was left working." }
    }
}

function Remove-Router {
    $address = Get-RouterAddress
    $flag = ''
    if ($Purge) { $flag = ' --purge' }
    Say "Removing Fengard from the router at $address"
    $remote = "if [ -f /etc/fengard/uninstall.sh ]; then sh /etc/fengard/uninstall.sh$flag; " +
        "elif [ -f /etc/fengard/router-uninstall.sh ]; then sh /etc/fengard/router-uninstall.sh$flag; " +
        'else echo Fengard is not installed on this router.; fi'
    $code = Invoke-SshWithRetry $address $remote
    if ($code -ne 0) { Fail "Removing Fengard failed (code $code)." }
}

function Get-ComputerBinary {
    $arch = $env:PROCESSOR_ARCHITEW6432
    if (-not $arch) { $arch = $env:PROCESSOR_ARCHITECTURE }
    $name = 'windows-amd64'
    if ($arch -eq 'ARM64') { $name = 'windows-arm64' }
    $bin = Join-Path $Kit "bin\$name\fengardd.exe"
    if (-not (Test-Path $bin)) {
        Fail "The Fengard program for this PC isn't in the kit ($bin). Use the downloaded Fengard kit, or build it with: go run ./tools/release -version dev"
    }
    return $bin
}

function Get-PortUser([string]$proto, [int]$port, [string[]]$addresses) {
    if ($proto -eq 'udp') {
        $eps = Get-NetUDPEndpoint -LocalPort $port -ErrorAction SilentlyContinue
    } else {
        $eps = Get-NetTCPConnection -State Listen -LocalPort $port -ErrorAction SilentlyContinue
    }
    foreach ($e in @($eps)) {
        if ($null -eq $e) { continue }
        if ($addresses -contains $e.LocalAddress) {
            $proc = Get-Process -Id $e.OwningProcess -ErrorAction SilentlyContinue
            if ($proc -and $proc.ProcessName -eq 'fengardd') { continue }
            if ($proc) { return "$($proc.ProcessName) (process $($e.OwningProcess))" }
            return "process $($e.OwningProcess)"
        }
    }
    return $null
}

function Stop-ComputerFengard {
    if (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue) {
        Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
    }
    Get-Process -Name fengardd -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 1
}

function Install-Computer {
    $src = Get-ComputerBinary
    $route = Get-DefaultRoute
    if (-not $route) { Fail 'This PC is not connected to a network.' }
    $addr = Get-NetIPAddress -InterfaceIndex $route.ifIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue |
        Where-Object { $_.IPAddress -notlike '169.254.*' } | Select-Object -First 1
    if (-not $addr) { Fail 'Could not find this PC''s network address.' }
    $ip = $addr.IPAddress

    Say 'Stopping any running Fengard'
    Stop-ComputerFengard

    Say 'Checking ports'
    $here = @('0.0.0.0', '::', $ip, '127.0.0.1')
    $dnsUser = Get-PortUser 'udp' 53 $here
    if ($dnsUser) { Fail "Another program already answers DNS on this PC: $dnsUser. Turn it off (for example Internet Connection Sharing) and run this again." }
    $httpPort = 80
    $httpsPort = 443
    $u = Get-PortUser 'tcp' 80 $here
    if ($u) {
        $httpPort = 8080
        Warn "Port 80 is used by $u, so the dashboard moves to port $httpPort and blocked sites show a browser error instead of the block page."
    }
    $u = Get-PortUser 'tcp' 443 $here
    if ($u) { $httpsPort = 8443; Warn "Port 443 is used by $u; HTTPS moves to port $httpsPort." }

    Say "Installing to $InstallDir"
    New-Item -ItemType Directory -Force -Path $InstallDir, $DataDir | Out-Null
    $exe = Join-Path $InstallDir 'fengardd.exe'
    Copy-Item -Force $src $exe
    Unblock-File -Path $exe -ErrorAction SilentlyContinue

    $fwArgs = "-dns `"$($ip):53,127.0.0.1:53`" -http `"$($ip):$httpPort`" -https `"$($ip):$httpsPort`" -block-ip $ip -data `"$DataDir`" -leases `"`""

    Say 'Allowing devices on the local network to reach Fengard (Windows Firewall)'
    Get-NetFirewallRule -Group 'Fengard' -ErrorAction SilentlyContinue | Remove-NetFirewallRule
    New-NetFirewallRule -DisplayName 'Fengard (DNS, dashboard, block page)' -Group 'Fengard' -Direction Inbound `
        -Program $exe -Action Allow -RemoteAddress LocalSubnet -Profile Any | Out-Null

    Say 'Starting Fengard now and at every boot (scheduled task, runs without anyone logged in)'
    $action = New-ScheduledTaskAction -Execute $exe -Argument $fwArgs -WorkingDirectory $DataDir
    $trigger = New-ScheduledTaskTrigger -AtStartup
    $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable `
        -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1)
    $principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
    Register-ScheduledTask -TaskName $TaskName -Description 'Fengard network protection (DNS-only mode)' `
        -Action $action -Trigger $trigger -Settings $settings -Principal $principal -Force | Out-Null
    Start-ScheduledTask -TaskName $TaskName

    $ok = $false
    for ($i = 0; $i -lt 40; $i++) {
        Start-Sleep -Seconds 1
        try {
            $r = Resolve-DnsName -Name 'fengard.lan' -Server '127.0.0.1' -DnsOnly -QuickTimeout -ErrorAction Stop
            if ($r | Where-Object { $_.IPAddress -eq $ip }) { $ok = $true; break }
        } catch { }
    }
    if (-not $ok) {
        Warn 'Fengard did not start answering DNS. Its output:'
        $out = Join-Path $env:TEMP 'fengard-start.log'
        Stop-ComputerFengard
        $p = Start-Process -FilePath $exe -ArgumentList $fwArgs -WorkingDirectory $DataDir -NoNewWindow -PassThru -RedirectStandardError $out
        Start-Sleep -Seconds 6
        if (-not $p.HasExited) { $p.Kill() }
        Get-Content $out -ErrorAction SilentlyContinue | Select-Object -Last 20 | ForEach-Object { Write-Host "   $_" }
        Fail 'Fengard could not start on this PC.'
    }

    $dash = "http://$ip/"
    if ($httpPort -ne 80) { $dash = "http://$($ip):$httpPort/" }
    Write-Host ''
    Say 'Fengard is running on this PC.'
    Note "Dashboard   $dash  (create the admin account there)"
    Note "DNS server  $ip"
    Write-Host ''
    Write-Host 'One step left, in your router''s admin page:' -ForegroundColor Green
    Note "1. Set the DNS server it gives out by DHCP (often under LAN or DHCP settings) to $ip."
    Note '   Give only that one address, with no second DNS server, or devices can go around Fengard.'
    Note '2. Reserve this PC''s address so it never changes (DHCP reservation / static lease).'
    Note '3. Reconnect your devices (or wait for them to renew) so they pick up the new DNS server.'
    if ($addr.PrefixOrigin -eq 'Dhcp') { Warn "This PC's address $ip comes from DHCP and could change; step 2 matters." }
    Write-Host ''
    Note 'Keep this PC on and awake: while it sleeps, devices using it lose internet name lookups.'
    Note 'DNS-only mode filters every device, but it has no firewall: port forwards, bypass blocking'
    Note 'and the VPN need Fengard installed on the router itself.'
}

function Remove-Computer {
    Say 'Removing Fengard from this PC'
    Stop-ComputerFengard
    if (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue) {
        Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
    }
    Get-NetFirewallRule -Group 'Fengard' -ErrorAction SilentlyContinue | Remove-NetFirewallRule
    if (Test-Path $InstallDir) { Remove-Item -Recurse -Force $InstallDir }
    if ($Purge) {
        if (Test-Path $DataDir) { Remove-Item -Recurse -Force $DataDir }
        Note 'Removed Fengard''s settings and history.'
    } elseif (Test-Path $DataDir) {
        Note "Kept settings and history in $DataDir (use -Purge to remove them)."
    }
    Note 'Remember to set your router''s DHCP DNS server back to automatic.'
}

function Show-Menu {
    Write-Host ''
    Write-Host 'Fengard setup' -ForegroundColor Cyan
    Write-Host ''
    Write-Host '  1  Install on my router        OpenWrt-based routers, including GL.iNet (recommended)'
    Write-Host '  2  Run on this computer        protects the whole network through DNS; this PC stays on'
    Write-Host '  3  Remove from my router'
    Write-Host '  4  Remove from this computer'
    Write-Host ''
    switch (Read-Host 'Choose 1-4') {
        '1' { return 'router' }
        '2' { return 'computer' }
        '3' { return 'remove-router' }
        '4' { return 'remove-computer' }
        default { return '' }
    }
}

$exitCode = 0
try {
    if (-not $Action) { $Action = Show-Menu }
    if (-not $Action) { Fail 'Nothing chosen.' }
    if ($Action -in @('computer', 'remove-computer') -and -not (Test-Admin)) {
        # ports 53 80 443 and the startup task need admin
        Say 'Asking for Administrator rights...'
        $argList = "-NoProfile -ExecutionPolicy Bypass -File `"$PSCommandPath`" -Action $Action"
        if ($Purge) { $argList += ' -Purge' }
        $p = Start-Process -FilePath 'powershell.exe' -ArgumentList $argList -Verb RunAs -Wait -PassThru
        $NoPause = $true
        exit $p.ExitCode
    }
    switch ($Action) {
        'router' { Install-Router }
        'remove-router' { Remove-Router }
        'computer' { Install-Computer }
        'remove-computer' { Remove-Computer }
    }
} catch {
    Write-Host ''
    Write-Host "ERROR: $($_.Exception.Message)" -ForegroundColor Red
    $exitCode = 1
} finally {
    if (-not $NoPause) {
        Write-Host ''
        Read-Host 'Press Enter to close' | Out-Null
    }
}
exit $exitCode
