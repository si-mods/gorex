package rex

import (
	"sync/atomic"
	"time"
)

// duration is a timeout that tests may shorten while servers and clients
// of earlier tests still run.
type duration struct{ v atomic.Int64 }

func newDuration(d time.Duration) *duration {
	x := &duration{}
	x.v.Store(int64(d))
	return x
}

func (d *duration) get() time.Duration { return time.Duration(d.v.Load()) }

func (d *duration) set(v time.Duration) time.Duration { return time.Duration(d.v.Swap(int64(v))) }

// SetTimeoutsForTest sets how long the servers of this process wait for a
// request of a connection kept alive, and for a remote client to take
// what they write (keepalive), and how long its clients wait for an
// answer (call); it returns a function that restores them. For the tests
// of other packages.
func SetTimeoutsForTest(keepalive, call time.Duration) (restore func()) {
	k, w, c := keepaliveTimeout.set(keepalive), writeTimeout.set(keepalive), callTimeout.set(call)
	return func() {
		keepaliveTimeout.set(k)
		writeTimeout.set(w)
		callTimeout.set(c)
	}
}
