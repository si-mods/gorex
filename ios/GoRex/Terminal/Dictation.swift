import AVFoundation
import FluidAudio
import os

/// Dictation, after rootshell's: the mic records, and SenseVoiceSmall
/// (FunASR's, in Core ML through FluidAudio) recognises it on the phone, in
/// the language it hears, with punctuation and digits. The app does not
/// ship the models: the mic's first tap asks, and downloads them.
@Observable final class Dictation {
    static let shared = Dictation()

    /// The encoder: int8, on the Neural Engine. Off it, as in the
    /// simulator, int8 gives NaN: fp16 there, as this FluidAudio's fp32
    /// takes 108 s of audio and no less.
    nonisolated private static var precision: SenseVoiceEncoderPrecision {
        #if targetEnvironment(simulator)
        .fp16
        #else
        .int8
        #endif
    }
    /// What the models weigh: preprocessor, encoder, vocabulary and VAD.
    static let size: Int64 = precision == .fp16 ? 474_000_000 : 241_000_000
    /// Where FluidAudio keeps them, out of backups.
    nonisolated private static let root = URL.applicationSupportDirectory.appending(path: "FluidAudio")
    nonisolated private static var speechDir: URL { MLModelConfigurationUtils.defaultModelsDirectory(for: .senseVoiceSmall) }
    nonisolated private static var vadModel: URL {
        MLModelConfigurationUtils.defaultModelsDirectory(for: .vad).appending(path: ModelNames.VAD.sileroVadFile)
    }
    /// Hugging Face, and hf-mirror.com, a mirror run by others, for where
    /// Hugging Face can't be reached, as often from mainland China.
    nonisolated private static let hub = "https://huggingface.co"
    nonisolated private static let mirror = "https://hf-mirror.com"
    /// SenseVoice's text-norm: punctuation and digits (withitn).
    nonisolated private static let textNorm: Int32 = 14

    /// The models on disk, whole.
    private(set) var installed = Dictation.present()
    /// The microphone's last levels while the mic is on, 0 to 1, the
    /// newest last.
    var levels = [Float](repeating: 0, count: 5)
    /// How much of a download is done; nil when none runs.
    private(set) var progress: Double?
    var error: String?
    private var task: Task<Void, Never>?
    /// The models loaded, and what unloads them once idle.
    private var models: Task<(SenseVoiceManager, VadManager), Error>?
    private var idle: Task<Void, Never>?

    private init() {
        // Loading never downloads: only download does.
        ModelHub.offlineMode = true
    }

    func download() {
        error = nil
        progress = 0
        task = Task {
            do {
                try await Self.fetch { p in Task { @MainActor in if self.progress != nil { self.progress = p.fractionCompleted } } }
            } catch {
                if !Task.isCancelled { self.error = error.localizedDescription }
            }
            installed = Self.present()
            progress = nil
        }
    }

    func cancel() { task?.cancel() }

    /// Removes the models, or what a download left.
    func delete() {
        task?.cancel()
        models = nil
        try? FileManager.default.removeItem(at: Self.root)
        installed = false
    }

    /// A listening, as the mic goes on. The models load as it starts, what
    /// it hears waiting for them, and stay loaded a minute after it ends.
    func listen() -> Listening {
        idle?.cancel()
        let models = self.models ?? Task { try await Self.load() }
        self.models = models
        let listening = Listening(models)
        Task {
            _ = await listening.heard.result
            // A load that failed is tried again by the next.
            guard case .success = await models.result else {
                if self.models == models { self.models = nil }
                return
            }
            idle?.cancel()
            idle = Task {
                try? await Task.sleep(for: .seconds(60))
                if !Task.isCancelled { self.models = nil }
            }
        }
        return listening
    }

    /// Takes the level of samples the microphone heard.
    func hear(_ level: Float) {
        levels = levels.dropFirst() + [Self.level(of: level, after: levels.last ?? 0)]
    }

    /// The level to show for samples of an RMS: from -50 dB to -10 dB, as
    /// 0 to 1; up at once, and down by half.
    nonisolated static func level(of rms: Float, after last: Float) -> Float {
        let level = min(1, max(0, (20 * log10(max(rms, 1e-6)) + 50) / 40))
        return max(level, (last + level) / 2)
    }

    /// What is said so far: each phrase recognised for good, and the guess
    /// at the one still open.
    nonisolated struct Said: Equatable, Sendable {
        var phrases: [String] = []
        var open = ""
        /// Whether it is being polished (Polish), the mic off.
        var polishing = false
        /// All of it, written as spoken.
        var text: String { Dictation.joined(phrases + [open]) }
        /// What of text is for good, its beginning.
        var settled: String { Dictation.joined(phrases) }
    }

    /// Phrases, spaced as words are: none between Chinese or Japanese on
    /// both sides, as SenseVoice also spaces Japanese tokens.
    nonisolated static func joined(_ phrases: [String]) -> String {
        var out = ""
        var gap = ""
        for c in phrases.joined(separator: " ") {
            if c == " " { gap.append(c); continue }
            if let last = out.last, unspaced(last), unspaced(c) { gap = "" }
            out += gap
            out.append(c)
            gap = ""
        }
        return out.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    /// Han, kana and full-width punctuation, written without spaces.
    nonisolated private static func unspaced(_ c: Character) -> Bool {
        switch c.unicodeScalars.first?.value ?? 0 {
        case 0x3000...0x30FF, 0x31F0...0x31FF, 0x3400...0x4DBF, 0x4E00...0x9FFF,
             0xF900...0xFAFF, 0xFF00...0xFFEF, 0x20000...0x2FA1F: true
        default: false
        }
    }

    /// Whether the models are whole: FluidAudio's check for SenseVoice; the
    /// VAD's compiled model, with nothing left half downloaded.
    nonisolated private static func present() -> Bool {
        guard SenseVoiceModels.modelsExist(at: speechDir, precision: precision),
              FileManager.default.fileExists(atPath: vadModel.appending(path: "coremldata.bin").path) else { return false }
        let files = FileManager.default.enumerator(at: vadModel, includingPropertiesForKeys: nil)
        return !(files?.contains { ($0 as? URL)?.pathExtension == "partial" } ?? true)
    }

    @concurrent nonisolated private static func load() async throws -> (SenseVoiceManager, VadManager) {
        let models = try SenseVoiceModels.load(from: speechDir, precision: precision)
        return (SenseVoiceManager(models: models, language: 0, textNorm: textNorm), try await VadManager())
    }

    /// Downloads what is missing from Hugging Face, and from the mirror
    /// when Hugging Face does not answer in seconds or its download fails.
    /// FluidAudio checks each file against the size its source lists, and
    /// keeps it as .partial until it is whole.
    @concurrent nonisolated private static func fetch(_ progress: @escaping ProgressHandler) async throws {
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        var v = URLResourceValues()
        v.isExcludedFromBackup = true
        var dir = root
        try dir.setResourceValues(v)
        ModelHub.offlineMode = false
        defer { ModelHub.offlineMode = true }
        let sources = await reachable(hub) ? [hub, mirror] : [mirror]
        for source in sources {
            ModelRegistry.baseURL = source
            do {
                try await ModelHub.download(.vad, to: MLModelConfigurationUtils.defaultModelsDirectory())
                _ = try await SenseVoiceModels.download(precision: precision, progressHandler: progress)
                return
            } catch where source != sources.last && !Task.isCancelled {}
        }
    }

    /// Whether a host answers within seconds; one a network blocks does not.
    @concurrent nonisolated private static func reachable(_ base: String) async -> Bool {
        var r = URLRequest(url: URL(string: base)!, timeoutInterval: 6)
        r.httpMethod = "HEAD"
        return (try? await URLSession.shared.data(for: r)) != nil
    }
}

/// One listening, as rootshell's DictationRecognizer, with its defaults:
/// Silero VAD cuts what the mic hears into phrases as it comes; the phrase
/// open is recognised again as it grows, for what is said so far, and once
/// more as the speaker pauses, for good.
nonisolated final class Listening: Sendable {
    /// What is said so far, as it changes: the newest only.
    let said: AsyncStream<Dictation.Said>
    /// All that was said, as finish gives it.
    let heard: Task<String, Error>
    private let audio: AsyncStream<[Float]>.Continuation

    init(_ models: Task<(SenseVoiceManager, VadManager), Error>) {
        let (said, show) = AsyncStream.makeStream(of: Dictation.Said.self, bufferingPolicy: .bufferingNewest(1))
        let (audio, add) = AsyncStream.makeStream(of: [Float].self)
        self.said = said
        self.audio = add
        heard = Task { try await Self.run(audio, models, show) }
    }

    /// 16 kHz mono samples, in order; from the audio thread too.
    func add(_ samples: [Float]) { audio.yield(samples) }

    /// Stops listening, and gives all that was said, the phrase open
    /// recognised for good.
    func finish() async throws -> String {
        audio.finish()
        return try await heard.value
    }

    /// Stops listening, and recognises no more.
    func cancel() {
        audio.finish()
        heard.cancel()
    }

    /// Hears the audio in the VAD's chunks, one at a time: while it
    /// recognises, the audio waits.
    @concurrent private static func run(_ audio: AsyncStream<[Float]>, _ models: Task<(SenseVoiceManager, VadManager), Error>,
                                        _ show: AsyncStream<Dictation.Said>.Continuation) async throws -> String {
        defer { show.finish() }
        let (speech, vad) = try await models.value
        var ear = Ear(speech: speech, vad: vad, show: show)
        var pending: [Float] = []
        let chunk = VadManager.chunkSize
        for await samples in audio {
            try Task.checkCancellation()
            pending += samples
            while pending.count >= chunk {
                try await ear.hear(Array(pending.prefix(chunk)))
                pending.removeFirst(chunk)
            }
        }
        try Task.checkCancellation()
        try await ear.end(pending)
        return ear.said.settled
    }

    /// The phrase open, and how it is heard.
    private nonisolated struct Ear {
        let speech: SenseVoiceManager
        let vad: VadManager
        let show: AsyncStream<Dictation.Said>.Continuation
        var said = Dictation.Said()
        private var state = VadStreamState.initial()
        /// What came before speech, for its first syllable: 0.4 s.
        private var before: [Float] = []
        private var phrase: [Float] = []
        private var speaking = false
        /// Whether the phrase open goes on from one cut at its limit.
        private var cut = false
        /// Samples since the last guess, and how many between guesses.
        private var heard = 0
        private var every = 9_600

        /// A pause of 0.8 s ends a phrase; speech starts at 0.6 and ends below 0.45.
        private static let pause = VadSegmentationConfig(minSilenceDuration: 0.8, silenceThresholdForSplit: 0.45,
                                                         negativeThreshold: 0.45, negativeThresholdOffset: 0.15)
        /// SenseVoice's encoder takes 0.2 to 30 s: a phrase is cut as it
        /// passes 29 s, its last chunk keeping it under 30.
        private static let longest = 29 * 16_000
        /// Shorter than 0.3 s, a phrase is a noise; a phrase cut goes on
        /// with a word of 0.2 s.
        private static let shortest = 4_800

        init(speech: SenseVoiceManager, vad: VadManager, show: AsyncStream<Dictation.Said>.Continuation) {
            self.speech = speech
            self.vad = vad
            self.show = show
        }

        mutating func hear(_ chunk: [Float]) async throws {
            let r = try await vad.processStreamingChunk(chunk, state: state, config: Self.pause)
            state = r.state
            if r.event?.kind == .speechStart, !speaking {
                speaking = true
                phrase = before
                before = []
                heard = 0
            }
            if speaking {
                phrase += chunk
                heard += chunk.count
            } else {
                before = Array((before + chunk).suffix(6_400))
            }
            if r.event?.kind == .speechEnd, speaking {
                speaking = false
                try await settle(last: true)
            } else if speaking, phrase.count >= Self.longest {
                try await settle(last: false)
            } else if speaking, heard >= every {
                await guess()
            }
        }

        /// The end of the audio: the phrase open, with what is left.
        mutating func end(_ rest: [Float]) async throws {
            guard speaking else { return }
            phrase += rest
            try await settle(last: true)
        }

        /// The phrase open, recognised for a guess; less often as that nears
        /// real time, so audio never piles up.
        private mutating func guess() async {
            heard = 0
            guard phrase.count >= Self.shortest else { return }
            let start = ContinuousClock.now
            if let text = try? await recognise(phrase), !text.isEmpty {
                said.open = text
                show.yield(said)
            }
            let took = (ContinuousClock.now - start) / .seconds(1)
            every = min(32_000, max(9_600, Int(took * 16_000) * 3))
        }

        /// The phrase open, recognised for good, as it ends or is cut.
        private mutating func settle(last: Bool) async throws {
            let samples = phrase
            phrase = []
            heard = 0
            let shortest = cut ? 3_200 : Self.shortest
            cut = !last
            let text = samples.count < shortest ? "" : try await recognise(samples)
            said.open = ""
            if !text.isEmpty { said.phrases.append(text) }
            show.yield(said)
        }

        private func recognise(_ samples: [Float]) async throws -> String {
            let r = try await speech.transcribeDetailed(audio: samples)
            return r.language == "nospeech" ? "" : Dictation.joined([r.text])
        }
    }
}

/// The microphone while the mic is on: what it hears, as SenseVoice takes
/// it, 16 kHz mono floats, given as it comes. A call or a new route stops
/// it.
final class Recording {
    nonisolated static let format = AVAudioFormat(commonFormat: .pcmFormatFloat32, sampleRate: 16_000, channels: 1, interleaved: false)!
    private let engine = AVAudioEngine()

    /// Starts the microphone; heard takes what it hears, on the audio
    /// thread.
    init(_ heard: @escaping @Sendable ([Float]) -> Void) throws {
        let audio = AVAudioSession.sharedInstance()
        try audio.setCategory(.record)
        try audio.setActive(true)
        let input = engine.inputNode
        let format = input.outputFormat(forBus: 0)
        guard let c = Converter(from: format) else {
            try? audio.setActive(false, options: .notifyOthersOnDeactivation)
            throw RexError(code: "dictation", message: String(localized: "This device has no microphone."))
        }
        let converter = OSAllocatedUnfairLock(uncheckedState: c)
        input.installTap(onBus: 0, bufferSize: 4096, format: format) { @Sendable buffer, _ in
            heard(converter.withLockUnchecked { $0.convert(buffer) })
        }
        do {
            try engine.start()
        } catch {
            stop()
            throw error
        }
    }

    /// Stops the microphone; others' sound comes back.
    func stop() {
        engine.stop()
        engine.inputNode.removeTap(onBus: 0)
        try? AVAudioSession.sharedInstance().setActive(false, options: .notifyOthersOnDeactivation)
    }

    /// The microphone's format, converted.
    nonisolated struct Converter {
        private let converter: AVAudioConverter

        /// Nil for a microphone that hears nothing, as a simulator's.
        init?(from source: AVAudioFormat) {
            guard source.sampleRate > 0, source.channelCount > 0,
                  let c = AVAudioConverter(from: source, to: Recording.format) else { return nil }
            // The first channel of several: the microphone's.
            if source.channelCount > 1 { c.channelMap = [0] }
            converter = c
        }

        func convert(_ buffer: AVAudioPCMBuffer) -> [Float] {
            let frames = Double(buffer.frameLength) * Recording.format.sampleRate / buffer.format.sampleRate
            guard let out = AVAudioPCMBuffer(pcmFormat: Recording.format, frameCapacity: AVAudioFrameCount(frames.rounded(.up)) + 16) else { return [] }
            // The converter asks until it has nothing more: the buffer once.
            var given = false
            _ = converter.convert(to: out, error: nil) { _, status in
                status.pointee = given ? .noDataNow : .haveData
                defer { given = true }
                return given ? nil : buffer
            }
            guard let c = out.floatChannelData?[0] else { return [] }
            return Array(UnsafeBufferPointer(start: c, count: Int(out.frameLength)))
        }
    }
}
