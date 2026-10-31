// Package rexapi serves Rex's API to Rex clients, over GoRex's own
// session server: a GoRex workspace is a Rex session, a tab is a window,
// and a pane with a local session is a block.
//
// The wire is Rex's own. A control connection at /api/control carries
// JSON messages, a data connection at /api/data streams a block's output,
// and /api/help, /llms.txt and /llms.html describe the API. The package
// speaks revision 7, the revision of the documentation in testdata.
//
// The server reads the layout from the GoRex server the app keeps
// (internal/rex), maps it, and tells its clients of the changes it sees.
// Layout changes are not part of this phase: a method that would change
// it is recognized and answered with not_supported.
package rexapi
