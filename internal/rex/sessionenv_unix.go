//go:build darwin || linux

package rex

// fillRegistryEnv is a Windows affair (sessionenv_windows.go); a session
// elsewhere gets the server's environment alone.
func fillRegistryEnv(env []string, _ bool) []string { return env }
