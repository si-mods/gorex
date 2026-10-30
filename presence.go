package main

import "time"

// presentFor is how long ago the user may have last typed or moved the
// mouse on this machine for them to still count as at it. A window that
// keeps the focus on a machine nobody uses claims no session's size then.
const presentFor = 60 * time.Second

// userPresent tells whether the user is at this machine, from how long ago
// they last gave it input. A platform that cannot tell counts as present,
// where a claim works as it always did. Tests swap it for their own answer.
var userPresent = func() bool {
	idle, ok := userIdle()
	return !ok || idle < presentFor
}
