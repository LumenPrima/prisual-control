$ws = New-Object -ComObject WScript.Shell
$s = $ws.CreateShortcut("$env:USERPROFILE\Desktop\PTZ Shim.lnk")
$s.TargetPath = "cmd.exe"
$s.Arguments = "/c `"$env:USERPROFILE\prisual-control\start-shim.bat`""
$s.WorkingDirectory = "$env:USERPROFILE\prisual-control"
$s.Save()
Write-Host "Shortcut updated - now try pinning it"
