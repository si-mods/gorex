package rex

import (
	"fmt"
	"os"
	"strings"
)

// inspect returns the name, arguments and working directory of a process.
var inspect = func(pid int) (p procInfo) {
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
	return procEnvOf(pid)(name)
}

// procEnvOf returns procEnv for a process, which reads its environment
// once, at the first call.
func procEnvOf(pid int) func(name string) string {
	return envReader(func() []string {
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
		if err != nil {
			return nil
		}
		return strings.Split(string(b), "\x00")
	})
}

func sysctlString(string) string { return "" }

func sysctlUint64(string) uint64 { return 0 }
