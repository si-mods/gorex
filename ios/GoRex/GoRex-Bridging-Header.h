// What GhosttyKit's ghostty.h leaves out, as rootshell declares it.

/// The read end of what Ghostty writes for the program of a surface with
/// external I/O: the keys, the pastes and the answers it encodes.
int ghostty_surface_response_read_fd(void *surface);
