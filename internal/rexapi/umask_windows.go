package rexapi

// privateUmask does nothing on Windows, where the socket sits in the
// user's own profile and takes its permissions.
func privateUmask() (restore func()) { return func() {} }
