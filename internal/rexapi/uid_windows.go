package rexapi

// currentUID returns 0: Windows has no Unix user ID, as Rex reports.
func currentUID() int { return 0 }
