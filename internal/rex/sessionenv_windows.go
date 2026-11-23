//go:build windows

package rex

import (
	"sort"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// registryEnvFunc reads the persistent environment a new process gets: the
// machine's, overridden by the user's. Tests override it.
var registryEnvFunc = registryEnv

// registryEnv returns the environment of the registry's two Environment
// keys as NAME=value strings, the user's over the machine's where both
// name one.
func registryEnv() []string {
	type kv struct{ name, value string }
	merged := map[string]kv{}
	for _, k := range []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`},
		{registry.CURRENT_USER, `Environment`},
	} {
		key, err := registry.OpenKey(k.root, k.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		if names, err := key.ReadValueNames(0); err == nil {
			for _, n := range names {
				v, _, err := key.GetStringValue(n)
				if err != nil {
					continue
				}
				if x, err := registry.ExpandString(v); err == nil {
					v = x
				}
				merged[strings.ToUpper(n)] = kv{n, v}
			}
		}
		key.Close()
	}
	names := make([]string, 0, len(merged))
	for n := range merged {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, merged[n].name+"="+merged[n].value)
	}
	return out
}

// fillRegistryEnv appends the persistent variables env lacks, so a session
// sees a variable set after the server started, as a terminal the user
// opens would. PATH stays the server's: a merge could order its
// directories in a way the user never saw. The names sessionEnv manages
// itself stay too, GoRex's own among them (GOREX_*, MYGO_*). A session of
// another user (runAs) gets none: the hive read is the server's own.
func fillRegistryEnv(env []string, otherUser bool) []string {
	if otherUser {
		return env
	}
	present := make(map[string]bool, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		present[strings.ToUpper(k)] = true
	}
	for _, kv := range registryEnvFunc() {
		k, _, _ := strings.Cut(kv, "=")
		uk := strings.ToUpper(k)
		if uk == "" || uk == "PATH" || present[uk] ||
			strings.HasPrefix(uk, "GOREX_") || strings.HasPrefix(uk, "MYGO_") {
			continue
		}
		env = append(env, kv)
	}
	return env
}
