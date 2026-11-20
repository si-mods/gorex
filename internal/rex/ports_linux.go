//go:build linux

package rex

import (
	"encoding/binary"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// portPIDs returns root and its descendants, walked through procfs.
func portPIDs(root int) ([]int, bool) {
	if root <= 0 {
		return nil, false
	}
	seen := map[int]bool{root: true}
	out := []int{root}
	queue := []int{root}
	ok := true
	for len(queue) > 0 && len(out) < 4096 {
		pid := queue[0]
		queue = queue[1:]
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/task/" + strconv.Itoa(pid) + "/children")
		if err != nil {
			if pid == root {
				return out, false
			}
			continue
		}
		for _, f := range strings.Fields(string(b)) {
			c, err := strconv.Atoi(f)
			if err != nil || c <= 0 || seen[c] {
				continue
			}
			seen[c] = true
			out = append(out, c)
			queue = append(queue, c)
		}
	}
	return out, ok
}

// portListeners returns the TCP sockets that the processes listen on,
// from /proc/net and the processes' descriptors.
func portListeners(pids []int) ([]portSocket, bool) {
	ok := true
	owners := map[string]int{}
	for _, pid := range pids {
		dir := "/proc/" + strconv.Itoa(pid) + "/fd"
		entries, err := os.ReadDir(dir)
		if err != nil {
			ok = false
			continue
		}
		for _, e := range entries {
			link, err := os.Readlink(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			inode, found := strings.CutPrefix(link, "socket:[")
			if !found {
				continue
			}
			inode = strings.TrimSuffix(inode, "]")
			if _, seen := owners[inode]; !seen {
				owners[inode] = pid
			}
		}
	}
	var out []portSocket
	for _, name := range []string{"tcp", "tcp6"} {
		b, err := os.ReadFile("/proc/net/" + name)
		if err != nil {
			ok = false
			continue
		}
		for _, line := range strings.Split(string(b), "\n")[1:] {
			f := strings.Fields(line)
			if len(f) < 10 || f[3] != "0A" {
				continue
			}
			pid, held := owners[f[9]]
			if !held {
				continue
			}
			addr, port, good := parseProcNetAddr(f[1])
			if !good {
				continue
			}
			out = append(out, portSocket{PID: pid, Port: port, Address: addr, Protocol: "tcp"})
		}
	}
	return dedupeSockets(out), ok
}

// parseProcNetAddr reads the local address of /proc/net/tcp and tcp6,
// which is hex, the address first in network order by 4-byte groups,
// then the port.
func parseProcNetAddr(s string) (string, int, bool) {
	addrHex, portHex, found := strings.Cut(s, ":")
	if !found {
		return "", 0, false
	}
	port, err := strconv.ParseUint(portHex, 16, 16)
	if err != nil {
		return "", 0, false
	}
	raw, err := hex.DecodeString(addrHex)
	if err != nil || len(raw) != 4 && len(raw) != 16 {
		return "", 0, false
	}
	if len(raw) == 4 {
		v := binary.LittleEndian.Uint32(raw)
		return net.IPv4(byte(v>>24), byte(v>>16), byte(v>>8), byte(v)).String(), int(port), true
	}
	// Each 4-byte group is little-endian in the file.
	ip := make(net.IP, 16)
	for i := range 4 {
		v := binary.LittleEndian.Uint32(raw[i*4 : i*4+4])
		binary.BigEndian.PutUint32(ip[i*4:i*4+4], v)
	}
	return ip.String(), int(port), true
}
