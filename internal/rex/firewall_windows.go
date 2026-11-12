package rex

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// firewallScript reads the firewall's state through its COM API, which
// needs no administrator: the active profiles, and the enabled inbound
// TCP rules of any of them, as JSON (firewallState).
const firewallScript = `[Console]::OutputEncoding = [Text.Encoding]::UTF8
$p = New-Object -ComObject HNetCfg.FwPolicy2
$cur = $p.CurrentProfileTypes
$prof = foreach ($t in 1,2,4) { if ($cur -band $t) { [pscustomobject]@{Type=$t;Enabled=[bool]$p.FirewallEnabled($t);BlockAll=[bool]$p.BlockAllInboundTraffic($t);DefaultIn=[int]$p.DefaultInboundAction($t)} } }
$rules = foreach ($r in $p.Rules) { if ($r.Direction -eq 1 -and $r.Enabled -and ($r.Profiles -band $cur) -and ($r.Protocol -eq 6 -or $r.Protocol -eq 256)) { [pscustomobject]@{App=[Environment]::ExpandEnvironmentVariables([string]$r.ApplicationName);Proto=[int]$r.Protocol;Ports=[string]$r.LocalPorts;Profiles=[int]$r.Profiles;Action=[int]$r.Action;Local=[string]$r.LocalAddresses;Remote=[string]$r.RemoteAddresses;Service=[string]$r.ServiceName} } }
[pscustomobject]@{Profiles=@($prof);Rules=@($rules)} | ConvertTo-Json -Compress -Depth 3`

// firewallQuery tells whether an inbound TCP connection to this program on
// ip and port passes Windows Firewall. It runs PowerShell, hidden, for
// half a second or so: never call it under a lock or on the UI thread.
func firewallQuery(ip netip.Addr, port int) firewallResult {
	exe, err := os.Executable()
	if err != nil {
		return firewallUnknown
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	sys, err := windows.GetSystemDirectory()
	if err != nil {
		return firewallUnknown
	}
	u := utf16.Encode([]rune(firewallScript))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2*i:], c)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(sys, `WindowsPowerShell\v1.0\powershell.exe`),
		"-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(b))
	hideConsole(cmd)
	out, err := cmd.Output()
	var st firewallState
	if err == nil {
		err = json.Unmarshal(out, &st)
	}
	if err != nil {
		log.Printf("rex: firewall: %v", err)
		return firewallUnknown
	}
	return firewallDecide(st, exe, ip, port)
}

// AllowFirewall adds Windows Firewall's inbound rules for the relay of
// Reachable at (FirewallRules), as administrator: Windows asks the user
// first. It returns once they are added, or with why not.
func AllowFirewall() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err == nil {
		defer windows.CoUninitialize()
	}
	sys, err := windows.GetSystemDirectory()
	if err != nil {
		return err
	}
	file, params := firewallElevated(sys)
	const seeMaskNoCloseProcess, seeMaskNoAsync, seeMaskFlagNoUI = 0x40, 0x100, 0x400
	info := shellExecuteInfo{
		mask: seeMaskNoCloseProcess | seeMaskNoAsync | seeMaskFlagNoUI,
		verb: windows.StringToUTF16Ptr("runas"), file: windows.StringToUTF16Ptr(file),
		params: windows.StringToUTF16Ptr(params), show: windows.SW_HIDE,
	}
	info.size = uint32(unsafe.Sizeof(info))
	if ok, _, err := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info))); ok == 0 {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return errors.New("rex: not allowed: Windows asked, and it was declined")
		}
		return fmt.Errorf("rex: firewall: %v", err)
	}
	if info.process == 0 {
		return nil
	}
	defer windows.CloseHandle(info.process)
	if ev, _ := windows.WaitForSingleObject(info.process, 60_000); ev != windows.WAIT_OBJECT_0 {
		return errors.New("rex: firewall: netsh did not end")
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.process, &code); err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("rex: firewall: netsh failed (%d)", code)
	}
	return nil
}

var procShellExecuteExW = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

// shellExecuteInfo is SHELLEXECUTEINFOW.
type shellExecuteInfo struct {
	size                    uint32
	mask                    uint32
	hwnd                    windows.HWND
	verb, file, params, dir *uint16
	show                    int32
	instApp, idList         uintptr
	class                   *uint16
	keyClass                windows.Handle
	hotKey                  uint32
	iconOrMonitor           windows.Handle
	process                 windows.Handle
}
