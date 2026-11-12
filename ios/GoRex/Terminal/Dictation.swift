import AVFoundation
import FluidAudio
import SherpaOnnxC
import os

/// Dictation, after rootshell's: the mic records, and the model chosen
/// recognises it on the phone, in the language it hears, with punctuation
/// and digits: SenseVoiceSmall (FunASR's, in Core ML through FluidAudio), or
/// a larger one, through sherpa-onnx. The app does not ship the models: the
/// mic's first tap asks, and downloads the one chosen.
@Observable final class Dictation {
    static let shared = Dictation()

    /// A speech model, and where it comes from.
    nonisolated enum Model: String, CaseIterable, Sendable {
        case senseVoice, funASRNano, qwen3ASR

        var name: String {
            switch self {
            case .senseVoice: "SenseVoice Small"
            case .funASRNano: "Fun-ASR-Nano"
            case .qwen3ASR: "Qwen3-ASR 0.6B"
            }
        }

        /// sherpa-onnx's int8 conversion, in a Hugging Face repo at a
        /// commit: its files, and what each weighs. SenseVoice's is
        /// FluidAudio's.
        var repo: (name: String, commit: String, files: [(path: String, size: Int)])? {
            switch self {
            case .senseVoice: nil
            case .funASRNano: ("csukuangfj/sherpa-onnx-funasr-nano-int8-2025-12-30", "6f16bd378457e13f36ccf3910df9017f96c346fb",
                               [("encoder_adaptor.int8.onnx", 237_792_748), ("llm.int8.onnx", 600_356_593), ("embedding.int8.onnx", 155_584_380),
                                ("Qwen3-0.6B/tokenizer.json", 11_422_654), ("Qwen3-0.6B/vocab.json", 2_776_833), ("Qwen3-0.6B/merges.txt", 1_671_853)])
            case .qwen3ASR: ("csukuangfj2/sherpa-onnx-qwen3-asr-0.6B-int8-2026-03-25", "68818b2313fe77bd06f6a7c5068ff3ef59d02b8a",
                             [("conv_frontend.onnx", 44_148_281), ("encoder.int8.onnx", 182_491_662), ("decoder.int8.onnx", 755_914_231),
                              ("tokenizer/tokenizer_config.json", 12_487), ("tokenizer/vocab.json", 2_776_833), ("tokenizer/merges.txt", 1_671_853)])
            }
        }

        /// What it weighs: SenseVoice's preprocessor, encoder, vocabulary
        /// and VAD; another's files.
        var size: Int64 {
            guard let repo else { return Dictation.precision == .fp16 ? 474_000_000 : 241_000_000 }
            return Int64(repo.files.reduce(0) { $0 + $1.size })
        }

        /// The longest phrase it takes, in samples. SenseVoice's encoder
        /// takes 0.2 to 30 s, and Qwen3-ASR as much: a phrase is cut as it
        /// passes 29 s, its last chunk keeping it under 30. Fun-ASR-Nano
        /// left words out of 29 s, not of 20.
        var longest: Int { self == .funASRNano ? 20 * 16_000 : 29 * 16_000 }
    }

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
    /// Where FluidAudio keeps its models, and sherpa-onnx's, out of backups.
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

    /// The model the mic uses, as Settings chose it.
    var model = Model(rawValue: UserDefaults.standard.string(forKey: "dictationModel") ?? "") ?? .senseVoice {
        didSet {
            UserDefaults.standard.set(model.rawValue, forKey: "dictationModel")
            // The one loaded goes; the next listening loads this.
            models = nil
        }
    }
    /// The models on disk, whole.
    private(set) var installed = Set(Model.allCases.filter(Dictation.present))
    /// The microphone's last levels while the mic is on, 0 to 1, the
    /// newest last.
    var levels = [Float](repeating: 0, count: 5)
    /// How much of a download is done; nil when none runs.
    private(set) var progress: Double?
    /// The model downloading, or the last downloaded.
    private(set) var downloading: Model?
    var error: String?
    private var task: Task<Void, Never>?
    /// The model loaded, and what unloads it once idle.
    private var models: Task<(any Recogniser, VadManager), Error>?
    private var idle: Task<Void, Never>?

    private init() {
        // Loading never downloads: only download does.
        ModelHub.offlineMode = true
    }

    func download(_ m: Model) {
        error = nil
        progress = 0
        downloading = m
        task = Task {
            do {
                try await Self.fetch(m) { p in Task { @MainActor in if self.progress != nil { self.progress = p } } }
            } catch {
                if !Task.isCancelled { self.error = error.localizedDescription }
            }
            installed = Set(Model.allCases.filter(Self.present))
            progress = nil
        }
    }

    func cancel() { task?.cancel() }

    /// Removes a model, or what a download of it left; with the last, the
    /// VAD too.
    func delete(_ m: Model) {
        if downloading == m { task?.cancel() }
        if m == model { models = nil }
        try? FileManager.default.removeItem(at: Self.dir(m))
        installed.remove(m)
        if installed.isEmpty, downloading == m || progress == nil { try? FileManager.default.removeItem(at: Self.root) }
    }

    /// A listening, as the mic goes on. The model loads as it starts, what
    /// it hears waiting for it, and stays loaded a minute after it ends.
    func listen() -> Listening {
        idle?.cancel()
        let model = self.model
        let models = self.models ?? Task { try await Self.load(model) }
        self.models = models
        let listening = Listening(models, longest: model.longest)
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

    nonisolated private static func dir(_ m: Model) -> URL {
        guard let repo = m.repo else { return speechDir }
        return root.appending(path: repo.name)
    }

    /// Whether a model is whole: the VAD's compiled model, with nothing
    /// left half downloaded; FluidAudio's check for SenseVoice, and
    /// another's files.
    nonisolated private static func present(_ m: Model) -> Bool {
        guard FileManager.default.fileExists(atPath: vadModel.appending(path: "coremldata.bin").path),
              !(FileManager.default.enumerator(at: vadModel, includingPropertiesForKeys: nil)?.contains { ($0 as? URL)?.pathExtension == "partial" } ?? true)
        else { return false }
        guard let repo = m.repo else { return SenseVoiceModels.modelsExist(at: speechDir, precision: precision) }
        return whole(repo.files, in: dir(m))
    }

    /// Whether each file is in dir at its size; one downloading is not
    /// yet, as get keeps it as .partial.
    nonisolated static func whole(_ files: [(path: String, size: Int)], in dir: URL) -> Bool {
        files.allSatisfy { (try? dir.appending(path: $0.path).resourceValues(forKeys: [.fileSizeKey]).fileSize) == $0.size }
    }

    @concurrent nonisolated private static func load(_ m: Model) async throws -> (any Recogniser, VadManager) {
        let speech: any Recogniser = m == .senseVoice
            ? SenseVoiceManager(models: try SenseVoiceModels.load(from: speechDir, precision: precision), language: 0, textNorm: textNorm)
            : try Sherpa(m, in: dir(m))
        return (speech, try await VadManager())
    }

    /// Downloads what is missing from Hugging Face, and from the mirror
    /// when Hugging Face does not answer in seconds or its download fails.
    /// FluidAudio checks each file against the size its source lists, and
    /// keeps it as .partial until it is whole; get does the same.
    @concurrent nonisolated private static func fetch(_ m: Model, _ progress: @escaping @Sendable (Double) -> Void) async throws {
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
                if m == .senseVoice {
                    _ = try await SenseVoiceModels.download(precision: precision) { progress($0.fractionCompleted) }
                } else {
                    try await get(m, from: source, progress)
                }
                return
            } catch where source != sources.last && !Task.isCancelled {}
        }
    }

    /// A sherpa-onnx model's files not yet whole, as Maple gets its faces:
    /// by ranges, each added to the file's .partial, which goes on from
    /// what it has and takes its name once whole.
    @concurrent nonisolated private static func get(_ m: Model, from source: String, _ progress: @Sendable (Double) -> Void) async throws {
        guard let repo = m.repo else { return }
        let total = repo.files.reduce(0) { $0 + $1.size }
        var done = 0
        for f in repo.files {
            let file = dir(m).appending(path: f.path)
            if whole([f], in: dir(m)) {
                done += f.size
                continue
            }
            let part = file.appendingPathExtension("partial")
            try FileManager.default.createDirectory(at: part.deletingLastPathComponent(), withIntermediateDirectories: true)
            if !FileManager.default.fileExists(atPath: part.path) { FileManager.default.createFile(atPath: part.path, contents: nil) }
            let out = try FileHandle(forWritingTo: part)
            defer { try? out.close() }
            var have = Int(try out.seekToEnd())
            if have > f.size {
                try out.truncate(atOffset: 0)
                have = 0
            }
            done += have
            // The file's URL; after the first range, the signed one it
            // redirects to, signed again as it expires.
            let resolve = URL(string: "\(source)/\(repo.name)/resolve/\(repo.commit)/\(f.path)")!
            var url = resolve
            while have < f.size {
                let n = min(16 << 20, f.size - have)
                var req = URLRequest(url: url)
                req.setValue("bytes=\(have)-\(have + n - 1)", forHTTPHeaderField: "Range")
                let (data, res) = try await URLSession.shared.data(for: req)
                guard (res as? HTTPURLResponse)?.statusCode == 206, data.count == n else {
                    guard url != resolve else { throw URLError(.badServerResponse) }
                    url = resolve
                    continue
                }
                url = res.url ?? url
                try out.write(contentsOf: data)
                have += n
                done += n
                progress(Double(done) / Double(total))
            }
            try? FileManager.default.removeItem(at: file)
            try FileManager.default.moveItem(at: part, to: file)
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

    /// longest: the longest phrase the model takes, in samples.
    init(_ models: Task<(any Recogniser, VadManager), Error>, longest: Int) {
        let (said, show) = AsyncStream.makeStream(of: Dictation.Said.self, bufferingPolicy: .bufferingNewest(1))
        let (audio, add) = AsyncStream.makeStream(of: [Float].self)
        self.said = said
        self.audio = add
        heard = Task { try await Self.run(audio, models, longest, show) }
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
    @concurrent private static func run(_ audio: AsyncStream<[Float]>, _ models: Task<(any Recogniser, VadManager), Error>,
                                        _ longest: Int, _ show: AsyncStream<Dictation.Said>.Continuation) async throws -> String {
        defer { show.finish() }
        let (speech, vad) = try await models.value
        var ear = Ear(speech: speech, vad: vad, longest: longest, show: show)
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
        let speech: any Recogniser
        let vad: VadManager
        /// A phrase is cut as it passes this many samples.
        let longest: Int
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
        /// Shorter than 0.3 s, a phrase is a noise; a phrase cut goes on
        /// with a word of 0.2 s.
        private static let shortest = 4_800

        init(speech: any Recogniser, vad: VadManager, longest: Int, show: AsyncStream<Dictation.Said>.Continuation) {
            self.speech = speech
            self.vad = vad
            self.longest = longest
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
            } else if speaking, phrase.count >= longest {
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

        /// The phrase open, recognised for a guess; the slower that is, the
        /// less often, so it takes a third of the time at most and audio
        /// never piles up.
        private mutating func guess() async {
            heard = 0
            guard phrase.count >= Self.shortest else { return }
            let start = ContinuousClock.now
            if let text = try? await recognise(phrase), !text.isEmpty {
                said.open = text
                show.yield(said)
            }
            let took = (ContinuousClock.now - start) / .seconds(1)
            every = max(9_600, Int(took * 16_000) * 3)
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
            Dictation.joined([try await speech.recognise(samples)])
        }
    }
}

/// A speech model loaded: what it recognises in a phrase, 16 kHz mono
/// samples; "" for none.
nonisolated protocol Recogniser: Sendable {
    func recognise(_ samples: [Float]) async throws -> String
}

extension SenseVoiceManager: Recogniser {
    func recognise(_ samples: [Float]) throws -> String {
        let r = try transcribeDetailed(audio: samples)
        return r.language == "nospeech" ? "" : r.text
    }
}

/// sherpa-onnx's recogniser, for Fun-ASR-Nano and Qwen3-ASR, on two CPU
/// cores, as they were measured: greedy, without hotwords.
actor Sherpa: Recogniser {
    private nonisolated(unsafe) let recogniser: OpaquePointer

    /// Throws as sherpa-onnx can't load the model in dir.
    init(_ m: Dictation.Model, in dir: URL) throws {
        var c = SherpaOnnxOfflineRecognizerConfig()
        var paths: [UnsafeMutablePointer<CChar>] = []
        defer { paths.forEach { free($0) } }
        func path(_ p: String) -> UnsafePointer<CChar> {
            paths.append(strdup(dir.appending(path: p).path))
            return UnsafePointer(paths.last!)
        }
        c.model_config.num_threads = 2
        switch m {
        case .funASRNano:
            c.model_config.funasr_nano.encoder_adaptor = path("encoder_adaptor.int8.onnx")
            c.model_config.funasr_nano.llm = path("llm.int8.onnx")
            c.model_config.funasr_nano.embedding = path("embedding.int8.onnx")
            c.model_config.funasr_nano.tokenizer = path("Qwen3-0.6B")
            c.model_config.funasr_nano.itn = 1
        case .qwen3ASR:
            c.feat_config.feature_dim = 128
            c.model_config.qwen3_asr.conv_frontend = path("conv_frontend.onnx")
            c.model_config.qwen3_asr.encoder = path("encoder.int8.onnx")
            c.model_config.qwen3_asr.decoder = path("decoder.int8.onnx")
            c.model_config.qwen3_asr.tokenizer = path("tokenizer")
            c.model_config.qwen3_asr.max_new_tokens = 256
        case .senseVoice:
            break
        }
        guard let r = SherpaOnnxCreateOfflineRecognizer(&c) else { throw CocoaError(.fileReadCorruptFile) }
        recogniser = r
    }

    deinit { SherpaOnnxDestroyOfflineRecognizer(recogniser) }

    func recognise(_ samples: [Float]) -> String {
        guard let s = SherpaOnnxCreateOfflineStream(recogniser) else { return "" }
        defer { SherpaOnnxDestroyOfflineStream(s) }
        SherpaOnnxAcceptWaveformOffline(s, 16_000, samples, Int32(samples.count))
        SherpaOnnxDecodeOfflineStream(recogniser, s)
        guard let r = SherpaOnnxGetOfflineStreamResult(s) else { return "" }
        defer { SherpaOnnxDestroyOfflineRecognizerResult(r) }
        return r.pointee.text.map { String(cString: $0) } ?? ""
    }
}

/// The microphone while the mic is on: what it hears, as the models take
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
