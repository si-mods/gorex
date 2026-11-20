//go:build darwin

package rex

import (
	"encoding/binary"
	"net"
	"strconv"
	"unsafe"
)

// libproc's flavors of proc_pidinfo, and the values of the structs it
// fills, from bsd/sys/proc_info.h.
const (
	procPidListFDs       = 1
	procPidFDSocketInfo  = 3
	proxFDTypeSocket     = 2
	sockInfoTCP          = 2
	tsiStateListen       = 1
	iniIPv4              = 0x1
	iniIPv6              = 0x2
	socketFDInfoSize     = 792
	socketInfoKindOffset = 256
	socketInfoLport      = 268
	socketInfoVflag      = 288
	socketInfoLaddr      = 312
	socketInfoIfIndex    = 340
	socketInfoTCPState   = 344
)

// portPIDs returns root and its descendants, walked with libproc.
func portPIDs(root int) ([]int, bool) {
	if root <= 0 {
		return nil, false
	}
	procOnce.Do(loadLibproc)
	if procListchildpids == nil {
		return []int{root}, false
	}
	seen := map[int]bool{root: true}
	out := []int{root}
	queue := []int32{int32(root)}
	ok := true
	for len(queue) > 0 && len(out) < 4096 {
		pid := queue[0]
		queue = queue[1:]
		buf := make([]int32, 64)
		n := procListchildpids(pid, unsafe.Pointer(&buf[0]), int32(len(buf)*4))
		if n < 0 {
			ok = false
			continue
		}
		if int(n) > len(buf) {
			buf = make([]int32, n)
			n = procListchildpids(pid, unsafe.Pointer(&buf[0]), int32(len(buf)*4))
			if n < 0 {
				ok = false
				continue
			}
		}
		for _, c := range buf[:n] {
			if c > 0 && !seen[int(c)] {
				seen[int(c)] = true
				out = append(out, int(c))
				queue = append(queue, c)
			}
		}
	}
	return out, ok
}

// portListeners returns the TCP sockets that the processes listen on.
func portListeners(pids []int) ([]portSocket, bool) {
	procOnce.Do(loadLibproc)
	if procPidinfo == nil || procPidfdinfo == nil {
		return nil, false
	}
	ok := true
	var out []portSocket
	fdBytes := make([]byte, 4096)
	sock := make([]byte, socketFDInfoSize)
	for _, pid := range pids {
		n := procPidinfo(int32(pid), procPidListFDs, 0, unsafe.Pointer(&fdBytes[0]), int32(len(fdBytes)))
		if n < 0 {
			ok = false
			continue
		}
		if int(n) > len(fdBytes) {
			fdBytes = make([]byte, n)
			n = procPidinfo(int32(pid), procPidListFDs, 0, unsafe.Pointer(&fdBytes[0]), int32(len(fdBytes)))
			if n < 0 {
				ok = false
				continue
			}
		}
		for off := 0; off+8 <= int(n); off += 8 {
			fd := int32(binary.LittleEndian.Uint32(fdBytes[off : off+4]))
			typ := binary.LittleEndian.Uint32(fdBytes[off+4 : off+8])
			if typ != proxFDTypeSocket {
				continue
			}
			if procPidfdinfo(int32(pid), fd, procPidFDSocketInfo, unsafe.Pointer(&sock[0]), int32(len(sock))) <= 0 {
				ok = false
				continue
			}
			sk, good := darwinListener(pid, sock)
			if !good {
				continue
			}
			out = append(out, sk)
		}
	}
	return dedupeSockets(out), ok
}

// darwinListener reads one socket_fdinfo.
func darwinListener(pid int, buf []byte) (portSocket, bool) {
	kind := int(int32(binary.LittleEndian.Uint32(buf[socketInfoKindOffset : socketInfoKindOffset+4])))
	if kind != sockInfoTCP {
		return portSocket{}, false
	}
	state := int(int32(binary.LittleEndian.Uint32(buf[socketInfoTCPState : socketInfoTCPState+4])))
	if state != tsiStateListen {
		return portSocket{}, false
	}
	// The kernel keeps the port in network order in the first two bytes
	// of the int.
	port := int(binary.BigEndian.Uint16(buf[socketInfoLport : socketInfoLport+2]))
	if port <= 0 || port > 65535 {
		return portSocket{}, false
	}
	vflag := buf[socketInfoVflag]
	var addr string
	switch {
	case vflag&iniIPv4 != 0:
		// struct in4in6_addr keeps the IPv4 address in its last 4 bytes.
		ip := net.IP(append([]byte(nil), buf[socketInfoLaddr+12:socketInfoLaddr+16]...))
		addr = ip.String()
	case vflag&iniIPv6 != 0:
		ip := net.IP(append([]byte(nil), buf[socketInfoLaddr:socketInfoLaddr+16]...))
		addr = ip.String()
		if ip.IsLinkLocalUnicast() {
			ifindex := int32(binary.LittleEndian.Uint32(buf[socketInfoIfIndex : socketInfoIfIndex+4]))
			if ifindex > 0 {
				addr += "%" + strconv.Itoa(int(ifindex))
			}
		}
	default:
		return portSocket{}, false
	}
	return portSocket{PID: pid, Port: port, Address: addr, Protocol: "tcp"}, true
}
