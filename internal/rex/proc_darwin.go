package rex

import (
	"bytes"
	"encoding/binary"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"golang.org/x/sys/unix"
)

// libproc's functions, loaded on first use.
var (
	procOnce          sync.Once
	procPidinfo       func(pid, flavor int32, arg uint64, buf unsafe.Pointer, size int32) int32
	procPidfdinfo     func(pid, fd, flavor int32, buf unsafe.Pointer, size int32) int32
	procListchildpids func(ppid int32, buf unsafe.Pointer, size int32) int32
)

const (
	procPidVnodePathInfo = 9
	procPidTBSDInfo      = 3
	// struct proc_vnodepathinfo: two vnode_info_path, each a vnode_info
	// (152 bytes) and a path of MAXPATHLEN bytes; the first is the
	// current directory.
	vnodeInfoSize = 152
	maxPathLen    = 1024
)

func loadLibproc() {
	lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return
	}
	purego.RegisterLibFunc(&procPidinfo, lib, "proc_pidinfo")
	purego.RegisterLibFunc(&procPidfdinfo, lib, "proc_pidfdinfo")
	purego.RegisterLibFunc(&procListchildpids, lib, "proc_listchildpids")
}

// inspect returns the name, arguments and working directory of a process.
var inspect = func(pid int) (p procInfo) {
	if pid <= 0 {
		return
	}
	if k, err := unix.SysctlKinfoProc("kern.proc.pid", pid); err == nil {
		comm := k.Proc.P_comm[:]
		if i := bytes.IndexByte(comm, 0); i >= 0 {
			comm = comm[:i]
		}
		p.name = string(comm)
	}
	p.path, p.args, _ = procArgs(pid, false)
	procOnce.Do(loadLibproc)
	if procPidinfo != nil {
		var buf [2 * (vnodeInfoSize + maxPathLen)]byte
		if procPidinfo(int32(pid), procPidVnodePathInfo, 0, unsafe.Pointer(&buf[0]), int32(len(buf))) > 0 {
			path := buf[vnodeInfoSize : vnodeInfoSize+maxPathLen]
			if i := bytes.IndexByte(path, 0); i >= 0 {
				path = path[:i]
			}
			p.dir = string(path)
		}
		// struct proc_bsdinfo: pbi_uid is a uint32 at offset 20.
		var bsd [136]byte
		if procPidinfo(int32(pid), procPidTBSDInfo, 0, unsafe.Pointer(&bsd[0]), int32(len(bsd))) > 0 {
			p.uid = int(binary.LittleEndian.Uint32(bsd[20:24]))
		}
	}
	return p
}

// procArgs returns the executable's path, the arguments and the
// environment of a process, from kern.procargs2: argc, the executable's
// path, NULs, the arguments and then the environment, each ending in a
// NUL. withEnv reads the environment too.
func procArgs(pid int, withEnv bool) (exe string, args, env []string) {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(raw) <= 4 {
		return "", nil, nil
	}
	argc := int(binary.LittleEndian.Uint32(raw))
	rest := raw[4:]
	// The executable's path, then NULs up to the arguments.
	if i := bytes.IndexByte(rest, 0); i >= 0 {
		exe = string(rest[:i])
		rest = rest[i:]
	}
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	for len(rest) > 0 {
		i := bytes.IndexByte(rest, 0)
		if i < 0 {
			i = len(rest)
		}
		if len(args) < argc {
			args = append(args, string(rest[:i]))
		} else if !withEnv || i == 0 {
			// An empty string ends the environment.
			break
		} else {
			env = append(env, string(rest[:i]))
		}
		rest = rest[min(i+1, len(rest)):]
	}
	return exe, args, env
}

// procEnv returns the variable name of a process's environment, "" when
// it has none or does not tell.
func procEnv(pid int, name string) string {
	return procEnvOf(pid)(name)
}

// procEnvOf returns procEnv for a process, which reads its environment
// once, at the first call.
func procEnvOf(pid int) func(name string) string {
	return envReader(func() []string {
		_, _, env := procArgs(pid, true)
		return env
	})
}

func sysctlString(name string) string {
	s, _ := unix.Sysctl(name)
	return s
}

func sysctlUint64(name string) uint64 {
	v, _ := unix.SysctlUint64(name)
	return v
}
