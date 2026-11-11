import AVFoundation
import os

/// GoRex's voice, outside Rex's API: the phone is the microphone of a host
/// that has none. While the mic key is held, what the microphone hears goes
/// to /api/voice for a block, as raw PCM, which the host's rec gives Claude
/// Code's /voice. A server that takes it lists Voice.feature in its welcome.
enum Voice {
    /// The welcome's feature of a server that takes voice.
    static let feature = "gorex.voice"

    /// The audio rec gives: signed 16-bit little-endian, 16 kHz, mono.
    nonisolated static var format: AVAudioFormat {
        AVAudioFormat(commonFormat: .pcmFormatInt16, sampleRate: 16_000, channels: 1, interleaved: true)!
    }

    /// The bytes of each message: 100 ms.
    nonisolated static let chunk = 3200

    /// What the microphone hears, in rec's format, cut into messages.
    nonisolated struct PCM {
        private let converter: AVAudioConverter
        private var held = Data()

        /// Converts from a microphone's format; nil for one that hears
        /// nothing, as a simulator's without a microphone.
        init?(from source: AVAudioFormat) {
            guard source.sampleRate > 0, source.channelCount > 0,
                  let c = AVAudioConverter(from: source, to: Voice.format) else { return nil }
            converter = c
        }

        /// Takes a buffer heard, and returns the whole messages there are.
        mutating func add(_ buffer: AVAudioPCMBuffer) -> [Data] {
            let frames = Double(buffer.frameLength) * Voice.format.sampleRate / buffer.format.sampleRate
            guard let out = AVAudioPCMBuffer(pcmFormat: converter.outputFormat, frameCapacity: AVAudioFrameCount(frames.rounded(.up)) + 16) else { return [] }
            // The converter asks until it has nothing more: the buffer once.
            var given = false
            _ = converter.convert(to: out, error: nil) { _, status in
                status.pointee = given ? .noDataNow : .haveData
                defer { given = true }
                return given ? nil : buffer
            }
            if let samples = out.int16ChannelData?[0] {
                held.append(Data(bytes: samples, count: Int(out.frameLength) * 2))
            }
            var whole: [Data] = []
            while held.count >= Voice.chunk {
                whole.append(Data(held.prefix(Voice.chunk)))
                held = Data(held.dropFirst(Voice.chunk))
            }
            return whole
        }

        /// What is left, short of a message.
        mutating func rest() -> Data {
            defer { held = Data() }
            return held
        }
    }
}

/// One hold of the mic key: the microphone streamed to a block's
/// /api/voice, a message each 100 ms, until stop sends what is left and
/// closes. The audio session is for recording while it runs; others'
/// sound comes back after.
final class VoiceStream {
    /// Called once when the stream fails, as when the host can't be
    /// reached; the microphone has stopped.
    var onFail: (Error) -> Void = { _ in }
    private let engine = AVAudioEngine()
    private let task: URLSessionWebSocketTask
    /// The messages, in order, from the microphone's thread to the socket.
    private let chunks: AsyncStream<Data>
    private let out: AsyncStream<Data>.Continuation
    private var pcm: OSAllocatedUnfairLock<Voice.PCM>?
    private var running = false
    /// What tells that the microphone went: a call took it, or a new route
    /// stopped the engine.
    private var observers: [NSObjectProtocol] = []

    init(block: String, to base: URL, session: URLSession = .shared) {
        var c = URLComponents(url: base.rexURL("/api/voice"), resolvingAgainstBaseURL: false)!
        c.queryItems = [URLQueryItem(name: "block_id", value: block)]
        var r = URLRequest(url: c.url!)
        // As the control connection's: a path that carries nothing fails
        // in seconds.
        r.timeoutInterval = 10
        task = session.webSocketTask(with: r)
        (chunks, out) = AsyncStream.makeStream()
    }

    /// Starts the microphone, and the stream.
    func start() throws {
        let audio = AVAudioSession.sharedInstance()
        try audio.setCategory(.record)
        try audio.setActive(true)
        running = true
        do {
            let input = engine.inputNode
            let format = input.outputFormat(forBus: 0)
            guard let p = Voice.PCM(from: format) else {
                throw RexError(code: "voice", message: String(localized: "This device has no microphone."))
            }
            let pcm = OSAllocatedUnfairLock(uncheckedState: p)
            self.pcm = pcm
            let out = out
            input.installTap(onBus: 0, bufferSize: AVAudioFrameCount(format.sampleRate / 10), format: format) { @Sendable buffer, _ in
                for c in pcm.withLockUnchecked({ $0.add(buffer) }) { out.yield(c) }
            }
            try engine.start()
        } catch {
            stop()
            throw error
        }
        // The host would hear silence: the stream fails instead.
        let watched: [(Notification.Name, AnyObject)] = [(AVAudioSession.interruptionNotification, audio),
                                                        (.AVAudioEngineConfigurationChange, engine)]
        observers = watched.map { name, object in
            NotificationCenter.default.addObserver(forName: name, object: object, queue: .main) { [weak self] _ in
                MainActor.assumeIsolated { self?.interrupted() }
            }
        }
        task.resume()
        Task { [weak self, task, chunks] in
            do {
                for await c in chunks { try await task.send(.data(c)) }
                task.cancel(with: .normalClosure, reason: nil)
            } catch {
                task.cancel()
                self?.stop()
                self?.onFail(error)
            }
        }
    }

    /// The microphone went while the stream ran.
    private func interrupted() {
        guard running else { return }
        stop()
        onFail(RexError(code: "voice", message: String(localized: "The microphone was interrupted.")))
    }

    /// Stops the microphone, sends what is left, and closes once it has gone.
    func stop() {
        guard running else { return }
        running = false
        for o in observers { NotificationCenter.default.removeObserver(o) }
        observers = []
        engine.stop()
        engine.inputNode.removeTap(onBus: 0)
        try? AVAudioSession.sharedInstance().setActive(false, options: .notifyOthersOnDeactivation)
        if let rest = pcm?.withLockUnchecked({ $0.rest() }), !rest.isEmpty { out.yield(rest) }
        out.finish()
    }
}
