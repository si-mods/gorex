package rexapi

// currentUID returns 0: Windows has no Unix user ID, as Rex reports.
func currentUID() int { return 0 }

// unixAdmission is left out on Windows, whose socket gives no
// credentials: its file's permissions are the boundary, which revision
// 12 has no name for.
const unixAdmission = ""
