import AVFoundation
import Synchronization
import UIKit
import os

/// Dictation, after rootshell's: the mic records, and the model chosen
/// recognises it on the phone, in the language it hears, with punctuation
/// and digits: SenseVoiceSmall or Fun-ASR-Nano, both running natively on
/// Apple Core ML with Silero VAD. The app does not ship the models: the
/// mic's first tap asks, and downloads the one chosen from GitHub Releases.
@Observable final class Dictation {
    static let shared = Dictation()

    /// A precompiled Core ML tarball hosted on si-mods/gorex GitHub Releases.
    nonisolated struct ReleaseBundle: Sendable {
        let tag: String
        let asset: String
        let dirName: String
        let files: [(path: String, size: Int)]

        var url: URL {
            URL(string: "https://github.com/si-mods/gorex/releases/download/\(tag)/\(asset)")!
        }

        var totalBytes: Int64 {
            Int64(files.reduce(0) { $0 + $1.size })
        }
    }

    /// Silero VAD v5/v6 Core ML bundle shared by every dictation model.
    nonisolated static let vadRelease = ReleaseBundle(
        tag: "models-sensevoice-vad-coreml-v1",
        asset: "silero-vad-coreml-v1.tar.bz2",
        dirName: "silero-vad-coreml-v1",
        files: [
            ("SileroVAD.mlmodelc/analytics/coremldata.bin", 243),
            ("SileroVAD.mlmodelc/coremldata.bin", 460),
            ("SileroVAD.mlmodelc/model.mil", 15_500),
            ("SileroVAD.mlmodelc/weights/weight.bin", 1_239_488),
        ]
    )

    /// A speech model, and where it comes from.
    nonisolated enum Model: String, CaseIterable, Sendable {
        case senseVoice, funASRNano

        var name: String {
            switch self {
            case .senseVoice: "SenseVoice Small"
            case .funASRNano: "Fun-ASR-Nano"
            }
        }

        /// The model as a single precompiled Core ML tarball on GitHub Releases.
        var release: ReleaseBundle {
            switch self {
            case .senseVoice:
                ReleaseBundle(
                    tag: "models-sensevoice-vad-coreml-v1",
                    asset: "sensevoice-small-coreml-v1.tar.bz2",
                    dirName: "sensevoice-small-coreml-v1",
                    files: [
                        ("SenseVoiceSmall.mlmodelc/analytics/coremldata.bin", 243),
                        ("SenseVoiceSmall.mlmodelc/coremldata.bin", 420),
                        ("SenseVoiceSmall.mlmodelc/model.mil", 889_051),
                        ("SenseVoiceSmall.mlmodelc/weights/weight.bin", 236_355_582),
                        ("tokens.json", 352_064),
                    ]
                )
            case .funASRNano:
                ReleaseBundle(
                    tag: "models-funasr-nano-coreml-v1",
                    asset: "funasr-nano-coreml-v1.tar.bz2",
                    dirName: "funasr-nano-coreml-v1",
                    files: [
                        ("EncoderAdaptor.mlmodelc/analytics/coremldata.bin", 243),
                        ("EncoderAdaptor.mlmodelc/coremldata.bin", 398),
                        ("EncoderAdaptor.mlmodelc/model.mil", 908_120),
                        ("EncoderAdaptor.mlmodelc/weights/weight.bin", 235_347_456),
                        ("TokenEmbedding.mlmodelc/analytics/coremldata.bin", 243),
                        ("TokenEmbedding.mlmodelc/coremldata.bin", 384),
                        ("TokenEmbedding.mlmodelc/model.mil", 2_626),
                        ("TokenEmbedding.mlmodelc/weights/weight.bin", 155_886_528),
                        ("Qwen3Decoder.mlmodelc/analytics/coremldata.bin", 243),
                        ("Qwen3Decoder.mlmodelc/coremldata.bin", 576),
                        ("Qwen3Decoder.mlmodelc/model.mil", 1_132_528),
                        ("Qwen3Decoder.mlmodelc/weights/weight.bin", 335_795_200),
                        ("vocab.json", 2_776_833),
                        ("prompt_ids.json", 341),
                    ]
                )
            }
        }

        /// What it weighs on disk (compiled Core ML files).
        var size: Int64 { release.totalBytes }

        /// The longest phrase it takes, in samples. SenseVoice's encoder
        /// takes 0.2 to 30 s: a phrase is cut as it passes 29 s, its last
        /// chunk keeping it under 30. Fun-ASR-Nano cuts at 20 s to stay
        /// within its 512-token context window.
        var longest: Int { self == .funASRNano ? 20 * 16_000 : 29 * 16_000 }
    }

    /// Where Core ML speech & VAD models live on disk, excluded from backups.
    nonisolated private static let root = URL.applicationSupportDirectory.appending(path: "FluidAudio")
    nonisolated private static var vadDir: URL { root.appending(path: vadRelease.dirName) }
    nonisolated private static var vadModel: URL { vadDir.appending(path: "SileroVAD.mlmodelc") }

    /// The model the mic uses, as Settings chose it. Migrates retired
    /// `"qwen3ASR"` selections to `.funASRNano`.
    var model: Model = {
        let saved = UserDefaults.standard.string(forKey: "dictationModel") ?? ""
        if saved == "qwen3ASR" { return .funASRNano }
        return Model(rawValue: saved) ?? .senseVoice
    }() {
        didSet {
            UserDefaults.standard.set(model.rawValue, forKey: "dictationModel")
            // The one loaded goes; the next listening loads this.
            models = nil
        }
    }
    /// The models on disk, whole, as the last scan found them: none until
    /// the first, which starts with Dictation, ends (installedModels).
    private(set) var installed: Set<Model> = []
    /// The scan for the models on disk while one runs, and the count of
    /// those asked for: one that ends after a later one is passed over.
    private var scanning: Task<Void, Never>?
    private var scans = 0
    /// The microphone's last levels while the mic is on, 0 to 1, the
    /// newest last. Kept long enough for Composer Mode's full-width wave;
    /// the key bar's compact meter reads the trailing five.
    var levels = [Float](repeating: 0, count: 48)
    /// How much of a download is done; nil when none runs.
    private(set) var progress: Double?
    /// The model downloading, or the last downloaded.
    private(set) var downloading: Model?
    var error: String?
    private var task: Task<Void, Never>?
    /// The model loaded, and what unloads it once idle.
    var models: Task<(any Recogniser, CoreMLSileroVAD), Error>?
    private var idle: Task<Void, Never>?
    /// The listenings not yet ended, each recognising until its last
    /// phrase is done: while one is, the app going to the background keeps
    /// the model.
    var listenings = 0

    private init() {
        // A model weighs up to a gigabyte: it goes as memory runs short,
        // and as the app leaves the screen with the mic off, rather than a
        // minute later, when iOS may have ended the app for it.
        let center = NotificationCenter.default
        center.addObserver(forName: UIApplication.didReceiveMemoryWarningNotification, object: nil, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.unload() }
        }
        center.addObserver(forName: UIApplication.didEnterBackgroundNotification, object: nil, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated {
                guard let self, self.listenings == 0 else { return }
                self.unload()
            }
        }
        // A few file checks and a directory listing, off the main actor.
        scan()
    }

    /// Whether the last scan for the models on disk has ended.
    var scanned: Bool { scanning == nil }

    /// The models on disk, once the scan running ends: what the mic waits
    /// for, so that it never offers to download a model already there.
    func installedModels() async -> Set<Model> {
        while let s = scanning { await s.value }
        return installed
    }

    /// Scans the disk for the models whole on it, off the main actor; the
    /// last scan asked for wins over one before it that ends after it.
    @discardableResult private func scan() -> Task<Void, Never> {
        scans += 1
        let n = scans
        let t = Task {
            let found = await Self.onDisk()
            guard n == scans else { return }
            installed = found
            scanning = nil
        }
        scanning = t
        return t
    }

    /// The models whole on disk.
    @concurrent nonisolated static func onDisk() async -> Set<Model> {
        removeLegacyONNXCaches()
        return Set(Model.allCases.filter(present))
    }

    /// Whether a model is loaded, or loading.
    var loaded: Bool { models != nil }

    /// Unloads the model now. A listening still on keeps the one it has
    /// until it ends; the next loads it again.
    func unload() {
        idle?.cancel()
        idle = nil
        models = nil
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
            await scan().value
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
        try? FileManager.default.removeItem(at: Self.stage(m))
        installed.remove(m)
        guard scanning != nil else { return dropVAD(after: m) }
        // A scan begun before the files went may find them: one begun
        // after does not, and the VAD waits for it.
        scan()
        Task {
            _ = await installedModels()
            dropVAD(after: m)
        }
    }

    /// The VAD, with the last model deleted.
    private func dropVAD(after m: Model) {
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
        listenings += 1
        Task {
            _ = await listening.heard.result
            listenings -= 1
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

    /// Shows a meter's levels, each new one once, looking at most 30 times
    /// a second, until cancelled.
    func show(_ meter: Meter) -> Task<Void, Never> {
        Task {
            var count: UInt64 = 0
            while !Task.isCancelled {
                if let rms = meter.take(after: &count) { hear(rms) }
                try? await Task.sleep(for: .seconds(1.0 / 30))
            }
        }
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
        root.appending(path: m.release.dirName)
    }

    /// Where a release's archive and what it unpacks into live while it
    /// downloads: beside the model, never in it.
    nonisolated private static func stage(_ m: Model) -> URL {
        root.appending(path: m.release.dirName + ".release")
    }

    /// Reclaims legacy sherpa-onnx ONNX and FluidAudio Hugging Face caches left
    /// on disk by earlier builds.
    nonisolated private static func removeLegacyONNXCaches() {
        let fm = FileManager.default
        for rel in ["csukuangfj/sherpa-onnx-funasr-nano-int8-2025-12-30",
                    "csukuangfj/sherpa-onnx-funasr-nano-int8-2025-12-30.release",
                    "csukuangfj2/sherpa-onnx-qwen3-asr-0.6B-int8-2026-03-25",
                    "csukuangfj2/sherpa-onnx-qwen3-asr-0.6B-int8-2026-03-25.release",
                    "Models/sensevoice-small-coreml",
                    "Models/silero-vad-coreml"] {
            try? fm.removeItem(at: root.appending(path: rel))
        }
    }

    /// Whether the VAD's compiled model is on disk whole: what every model
    /// recognises a phrase through.
    nonisolated static var vadPresent: Bool {
        whole(vadRelease.files, in: vadDir)
    }

    /// Whether a model is whole: Silero VAD plus the model's compiled Core ML files.
    nonisolated static func present(_ m: Model) -> Bool {
        vadPresent && whole(m.release.files, in: dir(m))
    }

    /// Whether each file is in dir at its size; one downloading is not
    /// yet, as tarball keeps it as .partial.
    nonisolated static func whole(_ files: [(path: String, size: Int)], in dir: URL) -> Bool {
        files.allSatisfy { (try? dir.appending(path: $0.path).resourceValues(forKeys: [.fileSizeKey]).fileSize) == $0.size }
    }

    @concurrent nonisolated private static func load(_ m: Model) async throws -> (any Recogniser, CoreMLSileroVAD) {
        let speech: any Recogniser = switch m {
        case .senseVoice: try CoreMLSenseVoice(in: dir(m))
        case .funASRNano: try CoreMLFunASR(in: dir(m))
        }
        return (speech, try CoreMLSileroVAD(modelURL: vadModel))
    }

    /// Downloads Silero VAD (if missing) and the chosen speech model from
    /// GitHub Releases using resumable 4-connection range requests.
    @concurrent nonisolated private static func fetch(_ m: Model, _ progress: @escaping @Sendable (Double) -> Void) async throws {
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        var v = URLResourceValues()
        v.isExcludedFromBackup = true
        var dir = root
        try dir.setResourceValues(v)
        if !vadPresent {
            try await tarball(vadRelease, into: vadDir, stage: root.appending(path: vadRelease.dirName + ".release")) { p in
                progress(p * 0.05)
            }
        }
        let base = vadPresent ? 0.05 : 0.0
        try await tarball(m.release, into: Self.dir(m), stage: Self.stage(m)) { p in
            progress(base + (1.0 - base) * p)
        }
    }

    /// Downloads a precompiled Core ML tarball from GitHub Releases by ranges
    /// on four connections, then unpacks its verified files into `dir`.
    @concurrent nonisolated private static func tarball(_ release: ReleaseBundle, into dir: URL, stage: URL,
                                                        _ progress: @escaping @Sendable (Double) -> Void) async throws {
        let archive = stage.appending(path: release.asset).appendingPathExtension("partial")
        let unpacked = stage.appending(path: "unpacked")
        try FileManager.default.createDirectory(at: stage, withIntermediateDirectories: true)
        let url = release.url
        let size = try await length(of: url)
        var have = Int((try? archive.resourceValues(forKeys: [.fileSizeKey]).fileSize) ?? 0)
        if have > size {
            try FileManager.default.removeItem(at: archive)
            have = 0
        }
        if have < size {
            if !FileManager.default.fileExists(atPath: archive.path) { FileManager.default.createFile(atPath: archive.path, contents: nil) }
            let out = try FileHandle(forWritingTo: archive)
            defer { try? out.close() }
            try out.truncate(atOffset: UInt64(have))
            try out.seekToEnd()
            try await withThrowingTaskGroup(of: (Int, Data, URL).self) { g in
                var next = have
                var got: [Int: Data] = [:]
                while have < size {
                    while next < size, next - have < 3 * chunk {
                        let at = next, n = min(chunk, size - at)
                        g.addTask { try await range(at, n, of: url, else: url) }
                        next += n
                    }
                    let (at, data, _) = try await g.next()!
                    got[at] = data
                    while let data = got.removeValue(forKey: have) {
                        try out.write(contentsOf: data)
                        have += data.count
                        progress(0.9 * Double(have) / Double(size))
                    }
                }
            }
        }
        try? FileManager.default.removeItem(at: unpacked)
        try TarBz2.extract(release.files, from: archive, into: unpacked, strip: 1) { progress(0.9 + 0.1 * $0) }
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        for f in release.files {
            let from = unpacked.appending(path: f.path)
            let to = dir.appending(path: f.path)
            try FileManager.default.createDirectory(at: to.deletingLastPathComponent(), withIntermediateDirectories: true)
            try? FileManager.default.removeItem(at: to)
            try FileManager.default.moveItem(at: from, to: to)
        }
        try? FileManager.default.removeItem(at: stage)
    }

    /// How big a release's asset is: a range asked for its first byte is
    /// answered with the whole of it.
    @concurrent nonisolated private static func length(of url: URL) async throws -> Int {
        var r = URLRequest(url: url, timeoutInterval: 30)
        r.setValue("bytes=0-0", forHTTPHeaderField: "Range")
        // Only the headers: a server that passed the range over would send
        // the whole model, which does not fit in memory.
        let (bytes, res) = try await URLSession.shared.bytes(for: r)
        bytes.task.cancel()
        guard let http = res as? HTTPURLResponse,
              let field = http.value(forHTTPHeaderField: "Content-Range"),
              let total = Int(field.split(separator: "/").last ?? ""), total > 0
        else { throw URLError(.badServerResponse) }
        return total
    }

    /// What tarball asks for at once: 8 MB.
    nonisolated private static let chunk = 8 << 20
    /// A connection for each of tarball's four ranges: one session would carry
    /// them all on one HTTP/2 connection, no faster than one range.
    nonisolated private static let sessions = (0..<4).map { _ in URLSession(configuration: .ephemeral) }

    /// n bytes of a file from at, by its signed URL, or by resolve when
    /// that fails as signed URLs expire; with the signed URL it gave.
    @concurrent nonisolated private static func range(_ at: Int, _ n: Int, of url: URL, else resolve: URL) async throws -> (Int, Data, URL) {
        for u in url == resolve ? [url] : [url, resolve] {
            var req = URLRequest(url: u)
            req.setValue("bytes=\(at)-\(at + n - 1)", forHTTPHeaderField: "Range")
            let (data, res) = try await sessions[at / chunk % 4].data(for: req)
            if (res as? HTTPURLResponse)?.statusCode == 206, data.count == n { return (at, data, res.url ?? u) }
        }
        throw URLError(.badServerResponse)
    }

}

/// One listening, as rootshell's DictationRecognizer, with its defaults:
/// Silero VAD cuts what the mic hears into phrases as it comes; the phrase
/// open is recognised again as it grows while the mic is on, for what is
/// said so far, and once more as the speaker pauses, for good.
nonisolated final class Listening: Sendable {
    /// What is said so far, as it changes: the newest only.
    let said: AsyncStream<Dictation.Said>
    /// All that was said, as finish gives it.
    let heard: Task<String, Error>
    /// What the microphone heard, waiting for the model.
    private let audio: SampleRing

    /// longest: the longest phrase the model takes, in samples.
    init(_ models: Task<(any Recogniser, CoreMLSileroVAD), Error>, longest: Int) {
        let (said, show) = AsyncStream.makeStream(of: Dictation.Said.self, bufferingPolicy: .bufferingNewest(1))
        let audio = SampleRing(capacity: Self.backlog)
        self.said = said
        self.audio = audio
        heard = Task { try await Self.run(audio, models, longest, show) }
    }

    /// The most samples that wait for the model, a minute of audio: past
    /// them, what comes is dropped rather than held without end while a
    /// model falls behind.
    static let backlog = 60 * 16_000

    /// 16 kHz mono samples, in order, from one thread at a time: the audio
    /// thread's. False when they are dropped, the backlog full or the
    /// listening over.
    @discardableResult func add(_ samples: UnsafeBufferPointer<Float>) -> Bool {
        audio.write(samples)
    }

    @discardableResult func add(_ samples: [Float]) -> Bool {
        samples.withUnsafeBufferPointer { audio.write($0) }
    }

    /// Adds samples to the end of kept, and keeps its last n, in place.
    static func keep(_ n: Int, of kept: inout [Float], adding samples: [Float]) {
        kept += samples
        if kept.count > n { kept.removeFirst(kept.count - n) }
    }

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

    /// Hears the audio in the VAD's chunks, one at a time, each filled from
    /// the ring as the audio comes: while it recognises, the audio waits.
    @concurrent private static func run(_ audio: SampleRing, _ models: Task<(any Recogniser, CoreMLSileroVAD), Error>,
                                        _ longest: Int, _ show: AsyncStream<Dictation.Said>.Continuation) async throws -> String {
        defer { show.finish() }
        let (speech, vad) = try await models.value
        var ear = Ear(speech: speech, vad: vad, audio: audio, longest: longest, show: show)
        var chunk = [Float](repeating: 0, count: CoreMLSileroVAD.chunkSize)
        var filled = 0
        while true {
            try Task.checkCancellation()
            // Over before the read: what came before the end is read.
            let over = audio.finished
            let n = chunk.withUnsafeMutableBufferPointer { audio.read(into: UnsafeMutableBufferPointer(rebasing: $0[filled...])) }
            filled += n
            if filled == chunk.count {
                try await ear.hear(chunk)
                filled = 0
            } else if over {
                break
            } else {
                try await Task.sleep(for: poll)
            }
        }
        try Task.checkCancellation()
        try await ear.end(Array(chunk[..<filled]))
        return ear.said.settled
    }

    /// How long the audio is waited for when the ring has too little of it:
    /// about half a microphone's buffer.
    private static let poll = Duration.milliseconds(50)

    /// What work gives, or nil as the mic goes off, work then cancelled:
    /// a guess going on would keep finish waiting, then settle its phrase
    /// again.
    static func unlessOff<T: Sendable>(_ audio: SampleRing, _ work: @escaping @Sendable () async throws -> T) async throws -> T? {
        try await withThrowingTaskGroup(of: T?.self) { g in
            g.addTask { try await work() }
            g.addTask {
                while !audio.finished { try await Task.sleep(for: poll) }
                return nil
            }
            defer { g.cancelAll() }
            return try await g.next() ?? nil
        }
    }

    /// The phrase open, and how it is heard.
    private nonisolated struct Ear {
        let speech: any Recogniser
        let vad: CoreMLSileroVAD
        /// What the microphone heard: once it is off, no guess.
        let audio: SampleRing
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

        init(speech: any Recogniser, vad: CoreMLSileroVAD, audio: SampleRing, longest: Int, show: AsyncStream<Dictation.Said>.Continuation) {
            self.speech = speech
            self.vad = vad
            self.audio = audio
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
                Listening.keep(6_400, of: &before, adding: chunk)
            }
            if r.event?.kind == .speechEnd, speaking {
                speaking = false
                try await settle(last: true)
            } else if speaking, phrase.count >= longest {
                try await settle(last: false)
            } else if !audio.finished, speaking, heard >= every {
                // With the mic off, what is left is settled, not guessed at.
                await guess()
            }
        }

        /// The end of the audio: the phrase open, with what is left.
        mutating func end(_ rest: [Float]) async throws {
            guard speaking else { return }
            phrase += rest
            try await settle(last: true)
        }

        /// The phrase open, recognised for a guess, given up as the mic goes
        /// off; the slower that is, the less often, so it takes a third of
        /// the time at most and audio never piles up.
        private mutating func guess() async {
            heard = 0
            guard phrase.count >= Self.shortest else { return }
            let start = ContinuousClock.now
            let (speech, samples) = (speech, phrase)
            if let text = try? await Listening.unlessOff(audio, { Dictation.joined([try await speech.recognise(samples)]) }),
               !text.isEmpty {
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

/// The microphone while the mic is on: what it hears, as the models take
/// it, 16 kHz mono floats, given as it comes. A call or a new route stops
/// it. The audio session and the engine start and stop on a queue of their
/// own, in turn (Turns): both wait on the system's audio, and take no time
/// from the main thread; a stop asked for as it starts wins, and the next
/// start waits for it. The engine is made and used on that queue only.
nonisolated final class Recording: @unchecked Sendable {
    static let format = AVAudioFormat(commonFormat: .pcmFormatFloat32, sampleRate: 16_000, channels: 1, interleaved: false)!
    private static let queue = DispatchQueue(label: "dev.gorex.recording", qos: .userInitiated)
    /// The engine, once a start begins: a stop with none has nothing to
    /// stop.
    private var engine: AVAudioEngine?
    private let turns = Turns(on: Recording.queue)

    /// Starts the microphone, on its queue: heard takes what it hears, on
    /// the audio thread, until the next convert; done is told on the main
    /// actor whether it is on (false: a stop came first), or why it can't be.
    func start(_ heard: @escaping @Sendable (UnsafeBufferPointer<Float>) -> Void,
               done: @escaping @MainActor @Sendable (Result<Bool, any Error>) -> Void) {
        turns.start({ try self.begin(heard) }, done: done)
    }

    private func begin(_ heard: @escaping @Sendable (UnsafeBufferPointer<Float>) -> Void) throws {
        let engine = AVAudioEngine()
        self.engine = engine
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
            // Given inside the lock: the samples are the converter's.
            converter.withLockUnchecked { heard($0.convert(buffer)) }
        }
        do {
            try engine.start()
        } catch {
            halt()
            throw error
        }
    }

    /// Stops the microphone, or the start of it; others' sound comes back,
    /// soon after.
    func stop() {
        turns.stop { self.halt() }
    }

    private func halt() {
        guard let engine else { return }
        engine.stop()
        engine.inputNode.removeTap(onBus: 0)
        try? AVAudioSession.sharedInstance().setActive(false, options: .notifyOthersOnDeactivation)
    }

    /// The microphone's format, converted.
    nonisolated struct Converter {
        private let converter: AVAudioConverter
        /// What a buffer converts into: made for the first, and again only
        /// for one larger, not for each as the audio thread takes it.
        private(set) var out: AVAudioPCMBuffer?

        /// Nil for a microphone that hears nothing, as a simulator's.
        init?(from source: AVAudioFormat) {
            guard source.sampleRate > 0, source.channelCount > 0,
                  let c = AVAudioConverter(from: source, to: Recording.format) else { return nil }
            // The first channel of several: the microphone's.
            if source.channelCount > 1 { c.channelMap = [0] }
            converter = c
        }

        /// The buffer converted, in out: good until the next convert, so
        /// the audio thread makes no array for it.
        mutating func convert(_ buffer: AVAudioPCMBuffer) -> UnsafeBufferPointer<Float> {
            let frames = AVAudioFrameCount((Double(buffer.frameLength) * Recording.format.sampleRate / buffer.format.sampleRate).rounded(.up)) + 16
            if (out?.frameCapacity ?? 0) < frames { out = AVAudioPCMBuffer(pcmFormat: Recording.format, frameCapacity: frames) }
            guard let out else { return UnsafeBufferPointer(start: nil, count: 0) }
            out.frameLength = 0
            // The converter asks until it has nothing more: the buffer once.
            var given = false
            _ = converter.convert(to: out, error: nil) { _, status in
                status.pointee = given ? .noDataNow : .haveData
                defer { given = true }
                return given ? nil : buffer
            }
            guard let c = out.floatChannelData?[0] else { return UnsafeBufferPointer(start: nil, count: 0) }
            return UnsafeBufferPointer(start: c, count: Int(out.frameLength))
        }
    }
}

/// Work started and stopped in turn on a serial queue, as the microphone
/// is: a stop wins over a start asked for before it, which is passed over
/// if it has not begun, and stopped after it if it has.
nonisolated final class Turns: Sendable {
    private let queue: DispatchQueue
    private let stopped = Atomic<Bool>(false)

    init(on queue: DispatchQueue) {
        self.queue = queue
    }

    /// Queues start now, after what is queued; done is told, on the main
    /// actor, whether it ran (false: a stop came first) or what it threw.
    func start(_ start: @escaping @Sendable () throws -> Void, done: @escaping @MainActor @Sendable (Result<Bool, any Error>) -> Void) {
        queue.async { [self] in
            let r: Result<Bool, any Error> = stopped.load(ordering: .acquiring)
                ? .success(false)
                : Result { try start(); return true }
            Task { @MainActor in done(r) }
        }
    }

    /// Stops: a start not yet begun is passed over, and stop runs after one
    /// that has.
    func stop(_ stop: @escaping @Sendable () -> Void) {
        stopped.store(true, ordering: .releasing)
        queue.async(execute: stop)
    }
}

/// Samples from one thread to another, as the audio thread gives what the
/// microphone hears to the listening: lock-free, a writer and a reader,
/// in a buffer made once. The audio thread neither allocates nor waits on
/// a lock the reader could hold. A write that does not fit is dropped
/// whole, as is one after finish.
nonisolated final class SampleRing: @unchecked Sendable {
    let capacity: Int
    private let storage: UnsafeMutablePointer<Float>
    /// The samples written, and read, since the start: the writer alone
    /// stores the first, and the reader the second.
    private let written = Atomic<Int>(0)
    private let taken = Atomic<Int>(0)
    private let over = Atomic<Bool>(false)

    init(capacity: Int) {
        precondition(capacity > 0)
        self.capacity = capacity
        storage = .allocate(capacity: capacity)
    }

    deinit { storage.deallocate() }

    /// The samples waiting.
    var count: Int { written.load(ordering: .acquiring) - taken.load(ordering: .acquiring) }

    /// Whether no more is written: what was stays to be read.
    var finished: Bool { over.load(ordering: .acquiring) }

    func finish() { over.store(true, ordering: .releasing) }

    /// Writes samples after those before; false, and none written, when
    /// they do not all fit, or after finish. The writer only.
    @discardableResult func write(_ samples: UnsafeBufferPointer<Float>) -> Bool {
        guard !finished else { return false }
        let w = written.load(ordering: .relaxed)
        let n = samples.count
        guard n <= capacity - (w - taken.load(ordering: .acquiring)) else { return false }
        guard n > 0, let from = samples.baseAddress else { return true }
        let at = w % capacity
        let first = min(n, capacity - at)
        (storage + at).update(from: from, count: first)
        storage.update(from: from + first, count: n - first)
        written.store(w + n, ordering: .releasing)
        return true
    }

    /// Reads the oldest samples into out, as many as wait and fit; how
    /// many. The reader only.
    func read(into out: UnsafeMutableBufferPointer<Float>) -> Int {
        let r = taken.load(ordering: .relaxed)
        let n = min(out.count, written.load(ordering: .acquiring) - r)
        guard n > 0, let to = out.baseAddress else { return 0 }
        let at = r % capacity
        let first = min(n, capacity - at)
        to.update(from: storage + at, count: first)
        (to + first).update(from: storage, count: n - first)
        taken.store(r + n, ordering: .releasing)
        return n
    }
}

/// The microphone's level, from the audio thread to the main one, which
/// looks at it at most once a frame (Dictation.show): no task or allocation
/// for each buffer the microphone gives. A level is taken once, the newest,
/// so the bars fall as levels come, not as frames do.
nonisolated final class Meter: Sendable {
    /// The count of levels set, high, and the newest's bits, low: one word,
    /// so a level is never taken with another's count.
    private let latest = Atomic<UInt64>(0)

    /// Sets the newest RMS; one thread only, the audio thread.
    func set(_ rms: Float) {
        let count = (latest.load(ordering: .relaxed) >> 32) &+ 1
        latest.store(count << 32 | UInt64(rms.bitPattern), ordering: .releasing)
    }

    /// The newest level set since count, which it moves on; nil for none.
    func take(after count: inout UInt64) -> Float? {
        let v = latest.load(ordering: .acquiring)
        guard v >> 32 != count else { return nil }
        count = v >> 32
        return Float(bitPattern: UInt32(truncatingIfNeeded: v))
    }
}
