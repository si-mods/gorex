package rex

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// inspect returns the name, arguments and working directory of a process.
func inspect(pid int) (p procInfo) {
	if pid <= 0 {
		return
	}
	if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid)); err == nil {
		p.name = strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil {
		p.args = strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
	}
	p.dir, _ = os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	return p
}

// procEnv returns the variable name of a process's environment, "" when
// it has none or does not tell.
func procEnv(pid int, name string) string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return ""
	}
	return envValue(strings.Split(string(b), "\x00"), name)
}

// parentPID returns the parent of a process, 0 when unknown.
func parentPID(pid int) int {
	if pid <= 0 {
		return 0
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	// pid (comm) state ppid ...: comm may hold spaces and parentheses.
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 2 {
		return 0
	}
	ppid, _ := strconv.Atoi(f[1])
	return ppid
}

func sysctlString(string) string { return "" }

func sysctlUint64(string) uint64 { return 0 }
