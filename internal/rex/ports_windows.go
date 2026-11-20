//go:build windows

package rex

import (
	"encoding/binary"
	"net"
	"strconv"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// tcpTableOwnerPIDListener is TCP_TABLE_OWNER_PID_LISTENER.
	tcpTableOwnerPIDListener = 3
	// tcpTableIPv4 and tcpTableIPv6 are the address families of the
	// table.
	tcpTableIPv4 = 2
	tcpTableIPv6 = 23
)

var (
	iphlpapi            = windows.NewLazySystemDLL("iphlpapi.dll")
	getExtendedTcpTable = iphlpapi.NewProc("GetExtendedTcpTable")
)

// portPIDs returns root and its descendants, from a Toolhelp snapshot.
func portPIDs(root int) ([]int, bool) {
	if root <= 0 {
		return nil, false
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return []int{root}, false
	}
	defer windows.CloseHandle(snapshot)
	children := map[uint32][]int{}
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return []int{root}, false
	}
	for {
		children[entry.ParentProcessID] = append(children[entry.ParentProcessID], int(entry.ProcessID))
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}
	seen := map[int]bool{root: true}
	out := []int{root}
	queue := []int{root}
	for len(queue) > 0 && len(out) < 4096 {
		pid := queue[0]
		queue = queue[1:]
		for _, c := range children[uint32(pid)] {
			if c <= 0 || seen[c] {
				continue
			}
			seen[c] = true
			out = append(out, c)
			queue = append(queue, c)
		}
	}
	return out, true
}

// portListeners returns the TCP sockets that the processes listen on.
func portListeners(pids []int) ([]portSocket, bool) {
	want := make(map[int]bool, len(pids))
	for _, pid := range pids {
		want[pid] = true
	}
	ok := true
	var out []portSocket
	v4, good := tcpTable(tcpTableIPv4)
	if !good {
		ok = false
	} else {
		out = append(out, windowsRows(v4, false, want)...)
	}
	v6, good := tcpTable(tcpTableIPv6)
	if !good {
		ok = false
	} else {
		out = append(out, windowsRows(v6, true, want)...)
	}
	return dedupeSockets(out), ok
}

// tcpTable returns the listener table of an address family.
func tcpTable(af uint32) ([]byte, bool) {
	var size uint32
	r, _, _ := getExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0,
		uintptr(af), tcpTableOwnerPIDListener, 0)
	if r != uintptr(windows.ERROR_INSUFFICIENT_BUFFER) && r != 0 {
		return nil, false
	}
	if size == 0 {
		return nil, true
	}
	buf := make([]byte, size)
	r, _, _ = getExtendedTcpTable.Call(uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)), 0, uintptr(af), tcpTableOwnerPIDListener, 0)
	if r != 0 {
		return nil, false
	}
	return buf[:size], true
}

// windowsRows reads the rows of a table: a count, then one struct for
// each socket.
func windowsRows(buf []byte, v6 bool, want map[int]bool) []portSocket {
	if len(buf) < 4 {
		return nil
	}
	n := int(binary.LittleEndian.Uint32(buf[:4]))
	size := 24
	if v6 {
		size = 56
	}
	var out []portSocket
	for i := range n {
		off := 4 + i*size
		if off+size > len(buf) {
			break
		}
		row := buf[off : off+size]
		var pid int
		var addr string
		var port int
		if v6 {
			ip := net.IP(append([]byte(nil), row[0:16]...))
			addr = ip.String()
			if ip.IsLinkLocalUnicast() {
				if scope := binary.LittleEndian.Uint32(row[16:20]); scope != 0 {
					addr += "%" + strconv.FormatUint(uint64(scope), 10)
				}
			}
			port = int(binary.BigEndian.Uint16(row[20:22]))
			pid = int(binary.LittleEndian.Uint32(row[52:56]))
		} else {
			addr = net.IP(append([]byte(nil), row[4:8]...)).String()
			port = int(binary.BigEndian.Uint16(row[8:10]))
			pid = int(binary.LittleEndian.Uint32(row[20:24]))
		}
		if !want[pid] {
			continue
		}
		out = append(out, portSocket{PID: pid, Port: port, Address: addr, Protocol: "tcp"})
	}
	return out
}
