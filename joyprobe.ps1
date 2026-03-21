
# Joystick probe using winmm.dll — same API as the Go shim
# Shows raw axis values so we can find which axis the throttle slider maps to

Add-Type @"
using System;
using System.Runtime.InteropServices;

public class WinMM {
    [StructLayout(LayoutKind.Sequential)]
    public struct JOYINFOEX {
        public uint dwSize;
        public uint dwFlags;
        public uint dwXpos;
        public uint dwYpos;
        public uint dwZpos;
        public uint dwRpos;
        public uint dwUpos;
        public uint dwVpos;
        public uint dwButtons;
        public uint dwButtonNumber;
        public uint dwPOV;
        public uint dwReserved1;
        public uint dwReserved2;
    }

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    public struct JOYCAPSW {
        public ushort wMid;
        public ushort wPid;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 32)]
        public string szPname;
        public uint wXmin;
        public uint wXmax;
        public uint wYmin;
        public uint wYmax;
        public uint wZmin;
        public uint wZmax;
        public uint wNumButtons;
        public uint wPeriodMin;
        public uint wPeriodMax;
        public uint wRmin;
        public uint wRmax;
        public uint wUmin;
        public uint wUmax;
        public uint wVmin;
        public uint wVmax;
        public uint wCaps;
        public uint wMaxAxes;
        public uint wNumAxes;
        public uint wMaxButtons;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 32)]
        public string szRegkey;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 260)]
        public string szOEMVxD;
    }

    [DllImport("winmm.dll")]
    public static extern uint joyGetNumDevs();

    [DllImport("winmm.dll")]
    public static extern uint joyGetPosEx(uint uJoyID, ref JOYINFOEX pji);

    [DllImport("winmm.dll", CharSet = CharSet.Unicode)]
    public static extern uint joyGetDevCapsW(uint uJoyID, ref JOYCAPSW pjc, uint cbjc);
}
"@

$numDevs = [WinMM]::joyGetNumDevs()
Write-Host "Windows reports $numDevs joystick slot(s)"
Write-Host ""

$info = New-Object WinMM+JOYINFOEX
$info.dwSize = [System.Runtime.InteropServices.Marshal]::SizeOf($info)
$info.dwFlags = 0xFF  # JOY_RETURNALL

$found = $false
for ($id = 0; $id -lt $numDevs -and $id -lt 16; $id++) {
    $ret = [WinMM]::joyGetPosEx($id, [ref]$info)
    if ($ret -eq 0) {
        $caps = New-Object WinMM+JOYCAPSW
        $capSize = [System.Runtime.InteropServices.Marshal]::SizeOf($caps)
        [WinMM]::joyGetDevCapsW($id, [ref]$caps, $capSize) | Out-Null

        Write-Host "=== Joystick ${id}: $($caps.szPname) ==="
        Write-Host "  NumAxes: $($caps.wNumAxes)  NumButtons: $($caps.wNumButtons)"
        Write-Host ""
        Write-Host "  Axis ranges (from caps):"
        Write-Host "    X:  min=$($caps.wXmin)  max=$($caps.wXmax)"
        Write-Host "    Y:  min=$($caps.wYmin)  max=$($caps.wYmax)"
        Write-Host "    Z:  min=$($caps.wZmin)  max=$($caps.wZmax)"
        Write-Host "    R:  min=$($caps.wRmin)  max=$($caps.wRmax)"
        Write-Host "    U:  min=$($caps.wUmin)  max=$($caps.wUmax)"
        Write-Host "    V:  min=$($caps.wVmin)  max=$($caps.wVmax)"
        Write-Host ""

        Write-Host "  Move the throttle slider while watching..."
        Write-Host "  (Press Ctrl+C to stop)"
        Write-Host ""

        $prevLine = ""
        while ($true) {
            $info2 = New-Object WinMM+JOYINFOEX
            $info2.dwSize = [System.Runtime.InteropServices.Marshal]::SizeOf($info2)
            $info2.dwFlags = 0xFF
            $ret2 = [WinMM]::joyGetPosEx($id, [ref]$info2)
            if ($ret2 -ne 0) { break }

            $line = "  X=$($info2.dwXpos)  Y=$($info2.dwYpos)  Z=$($info2.dwZpos)  R=$($info2.dwRpos)  U=$($info2.dwUpos)  V=$($info2.dwVpos)  Btns=$($info2.dwButtons)"
            if ($line -ne $prevLine) {
                Write-Host "`r$line" -NoNewline
                $prevLine = $line
            }
            Start-Sleep -Milliseconds 50
        }

        $found = $true
        break
    }
}

if (-not $found) {
    Write-Host "No joystick connected!"
}
