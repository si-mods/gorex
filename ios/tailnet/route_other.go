//go:build !darwin

package tailnet

// setDefaultInterface does nothing off Apple's systems, where netmon
// watches the routes itself; the engine runs only on iOS.
func setDefaultInterface(string) {}
