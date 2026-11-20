import Foundation

/// Reads a file descriptor on a queue of its own, not the main actor's,
/// into one buffer for as long as it reads.
nonisolated enum FDReader {
    static let bufferSize = 16 << 10

    /// Passes what fd gives to deliver, as it comes. The reader is in group
    /// until it stops, by cancel or at the end of fd. The source runs its
    /// handlers one at a time on its queue, the cancel handler after the
    /// last event handler: that one frees the buffer.
    static func start(_ fd: Int32, _ group: DispatchGroup, _ deliver: @escaping @Sendable (Data) -> Void) -> DispatchSourceRead {
        _ = fcntl(fd, F_SETFL, fcntl(fd, F_GETFL) | O_NONBLOCK)
        let source = DispatchSource.makeReadSource(fileDescriptor: fd, queue: DispatchQueue(label: "dev.gorex.ios.terminal.input"))
        nonisolated(unsafe) let buf = UnsafeMutablePointer<UInt8>.allocate(capacity: bufferSize)
        group.enter()
        source.setCancelHandler {
            buf.deallocate()
            group.leave()
        }
        source.setEventHandler {
            var got = Data()
            while true {
                let n = Darwin.read(fd, buf, bufferSize)
                if n > 0 { got.append(buf, count: n); continue }
                if n == 0 { source.cancel() }
                break
            }
            if !got.isEmpty { deliver(got) }
        }
        source.resume()
        return source
    }
}
