import AVFoundation
import FluidAudio
import SherpaOnnxC
import Synchronization
import UIKit
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
            case .qwen3ASR: ("csukuangfj2/sherpa-onnx-qwen3-asr-0.6B-int8-2026-03-25", "68818b2313fe77bd06f6a7c5068ff3ef59d02b8a", qwen3)
            }
        }

        /// The same model as sherpa-onnx releases it, one tarball on
        /// GitHub: its asset, and the files in it with what each weighs.
        /// Those are that build's, which are not always the commit's:
        /// Fun-ASR-Nano's differ. Nil for SenseVoice's, which FluidAudio
        /// keeps.
        var release: (asset: String, files: [(path: String, size: Int)])? {
            switch self {
            case .senseVoice: nil
            case .funASRNano: ("sherpa-onnx-funasr-nano-int8-2025-12-30.tar.bz2",
                               [("encoder_adaptor.int8.onnx", 238_277_200), ("llm.int8.onnx", 600_339_316), ("embedding.int8.onnx", 155_583_106),
                                ("Qwen3-0.6B/tokenizer.json", 11_422_654), ("Qwen3-0.6B/vocab.json", 2_776_833), ("Qwen3-0.6B/merges.txt", 1_671_853)])
            case .qwen3ASR: ("sherpa-onnx-qwen3-asr-0.6B-int8-2026-03-25.tar.bz2", qwen3)
            }
        }

        /// Qwen3-ASR's files, the same in its commit and its release.
        private var qwen3: [(path: String, size: Int)] {
            [("conv_frontend.onnx", 44_148_281), ("encoder.int8.onnx", 182_491_662), ("decoder.int8.onnx", 755_914_231),
             ("tokenizer/tokenizer_config.json", 12_487), ("tokenizer/vocab.json", 2_776_833), ("tokenizer/merges.txt", 1_671_853)]
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
    /// Hugging Face can't be reached, as often from mainland China. The
    /// mirror now redirects there instead of serving the files itself, so a
    /// model both fail to serve comes as one tarball from GitHub.
    nonisolated private static let hub = "https://huggingface.co"
    nonisolated private static let mirror = "https://hf-mirror.com"
    /// sherpa-onnx's releases, where each of its models is one tarball.
    nonisolated private static let releases = "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models"
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
    /// The models on disk, whole, as the last scan found them: none until
    /// the first, which starts with Dictation, ends (installedModels).
    private(set) var installed: Set<Model> = []
    /// The scan for the models on disk while one runs, and the count of
    /// those asked for: one that ends after a later one is passed over.
    private var scanning: Task<Void, Never>?
    private var scans = 0
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
    var models: Task<(any Recogniser, VadManager), Error>?
    private var idle: Task<Void, Never>?
    /// The listenings not yet ended, each recognising until its last
    /// phrase is done: while one is, the app going to the background keeps
    /// the model.
    var listenings = 0

    private init() {
        // Loading never downloads: only download does.
        ModelHub.offlineMode = true
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
        Set(Model.allCases.filter(present))
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
        guard let repo = m.repo else { return speechDir }
        return root.appending(path: repo.name)
    }

    /// Where a release's archive and what it unpacks into live while it
    /// downloads: beside the model, never in it.
    nonisolated private static func stage(_ m: Model) -> URL {
        root.appending(path: (m.repo?.name ?? m.rawValue) + ".release")
    }

    /// Whether the VAD's compiled model is on disk whole: what every model
    /// recognises a phrase through.
    nonisolated static var vadPresent: Bool {
        FileManager.default.fileExists(atPath: vadModel.appending(path: "coremldata.bin").path)
            && !(FileManager.default.enumerator(at: vadModel, includingPropertiesForKeys: nil)?.contains { ($0 as? URL)?.pathExtension == "partial" } ?? true)
    }

    /// Whether a model is whole: the VAD's compiled model, with nothing
    /// left half downloaded; FluidAudio's check for SenseVoice, and
    /// another's files.
    nonisolated static func present(_ m: Model) -> Bool {
        guard vadPresent else { return false }
        guard let repo = m.repo else { return SenseVoiceModels.modelsExist(at: speechDir, precision: precision) }
        if whole(repo.files, in: dir(m)) { return true }
        // The release's build of the same model, whose files are not
        // always the commit's.
        return m.release.map { whole($0.files, in: dir(m)) } ?? false
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

    /// Downloads what is missing from the first source that serves it:
    /// Hugging Face, hf-mirror, or sherpa-onnx's release on GitHub, where
    /// the model is one tarball. FluidAudio checks each file against the
    /// size its source lists, and keeps it as .partial until it is whole;
    /// get and the release's tarball do the same.
    @concurrent nonisolated private static func fetch(_ m: Model, _ progress: @escaping @Sendable (Double) -> Void) async throws {
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        var v = URLResourceValues()
        v.isExcludedFromBackup = true
        var dir = root
        try dir.setResourceValues(v)
        ModelHub.offlineMode = false
        defer { ModelHub.offlineMode = true }
        let sources = await sources(m)
        guard !sources.isEmpty else {
            throw RexError(code: "dictation", message: m.release == nil
                           ? String(localized: "Hugging Face and its mirror could not be reached.")
                           : String(localized: "Hugging Face, its mirror and GitHub could not be reached."))
        }
        var failure: Error?
        for source in sources {
            ModelRegistry.baseURL = source.url
            do {
                // Listing the VAD's files on Hugging Face takes the network:
                // one already on disk is not asked for again, whatever the
                // source, its listing unreachable as much as its files.
                if !vadPresent {
                    try await ModelHub.download(.vad, to: MLModelConfigurationUtils.defaultModelsDirectory())
                }
                if m == .senseVoice {
                    _ = try await SenseVoiceModels.download(precision: precision) { progress($0.fractionCompleted) }
                } else if source.release {
                    try await tarball(m, progress)
                } else {
                    try await get(m, from: source.url, progress)
                }
                return
            } catch {
                if Task.isCancelled { throw error }
                failure = error
            }
        }
        // A source served, and failed: its error says the most about why.
        throw failure ?? URLError(.cannotConnectToHost)
    }

    /// A place a model comes from: Hugging Face or its mirror, file by
    /// file, or sherpa-onnx's release, the model as one tarball.
    nonisolated private struct Source: Sendable {
        let url: String
        let release: Bool
    }

    /// The sources that serve a model, best first. One that does not serve
    /// is left out, so a host this network blocks fails no download: from
    /// mainland China hf-mirror answers nothing but a redirect to Hugging
    /// Face, which does not answer at all.
    @concurrent nonisolated private static func sources(_ m: Model) async -> [Source] {
        async let fromHub = serves(hub, m)
        async let fromMirror = serves(mirror, m)
        var sources: [Source] = []
        if await fromHub { sources.append(Source(url: hub, release: false)) }
        if await fromMirror { sources.append(Source(url: mirror, release: false)) }
        if m.release != nil { sources.append(Source(url: hub, release: true)) }
        return sources
    }

    /// Whether a source serves a model: the first bytes of the smallest of
    /// its files come back, not a redirect to a host this network cannot
    /// reach. SenseVoice's is the VAD's model, which a source of a
    /// sherpa-onnx one carries too.
    @concurrent nonisolated private static func serves(_ base: String, _ m: Model) async -> Bool {
        let probe: (repo: String, commit: String, path: String, size: Int)
        if let repo = m.repo, let smallest = repo.files.min(by: { $0.size < $1.size }) {
            probe = (repo.name, repo.commit, smallest.path, smallest.size)
        } else {
            probe = (Repo.vad.rawValue, "main", "\(ModelNames.VAD.sileroVadFile)/coremldata.bin", 100)
        }
        let n = min(64, probe.size)
        guard let url = URL(string: "\(base)/\(probe.repo)/resolve/\(probe.commit)/\(probe.path)") else { return false }
        var r = URLRequest(url: url, timeoutInterval: 10)
        r.setValue("bytes=0-\(n - 1)", forHTTPHeaderField: "Range")
        guard let (data, res) = try? await sessions[0].data(for: r) else { return false }
        return (res as? HTTPURLResponse)?.statusCode == 206 && data.count == n
    }

    /// A sherpa-onnx model from GitHub's release: the whole of it as one
    /// tarball, resumed by ranges on four connections as get does a file,
    /// then unpacked where its files belong. Half of what the tarball holds
    /// is not wanted, and bzip2 gives no way to pass over it.
    @concurrent nonisolated private static func tarball(_ m: Model, _ progress: @escaping @Sendable (Double) -> Void) async throws {
        guard let release = m.release else { return }
        let dir = dir(m)
        // Its archive and what it unpacks into stand beside the model, so
        // that neither a half download nor a file of the release's build
        // lies beside the commit's, which get goes on from.
        let stage = Self.stage(m)
        let archive = stage.appending(path: release.asset).appendingPathExtension("partial")
        let unpacked = stage.appending(path: "unpacked")
        try FileManager.default.createDirectory(at: stage, withIntermediateDirectories: true)
        let url = URL(string: "\(releases)/\(release.asset)")!
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

    /// A sherpa-onnx model's files not yet whole, as Maple gets its faces:
    /// by ranges, four at a time on four connections as one is slow, each
    /// added in turn to the file's .partial, which goes on from what it
    /// has and takes its name once whole.
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
            // Ranges ahead of the .partial, those coming and those come
            // out of turn, are four at most.
            try await withThrowingTaskGroup(of: (Int, Data, URL).self) { g in
                var next = have
                var got: [Int: Data] = [:]
                while have < f.size {
                    while next < f.size, next - have < 4 * chunk {
                        let at = next, n = min(chunk, f.size - at), u = url
                        g.addTask { try await range(at, n, of: u, else: resolve) }
                        next += n
                    }
                    let (at, data, signed) = try await g.next()!
                    got[at] = data
                    url = signed
                    while let data = got.removeValue(forKey: have) {
                        try out.write(contentsOf: data)
                        have += data.count
                        done += data.count
                        progress(Double(done) / Double(total))
                    }
                }
            }
            try? FileManager.default.removeItem(at: file)
            try FileManager.default.moveItem(at: part, to: file)
        }
    }

    /// What get asks for at once: 8 MB.
    nonisolated private static let chunk = 8 << 20
    /// A connection for each of get's four ranges: one session would carry
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
/// open is recognised again as it grows, for what is said so far, and once
/// more as the speaker pauses, for good.
nonisolated final class Listening: Sendable {
    /// What is said so far, as it changes: the newest only.
    let said: AsyncStream<Dictation.Said>
    /// All that was said, as finish gives it.
    let heard: Task<String, Error>
    /// What the microphone heard, waiting for the model.
    private let audio: SampleRing

    /// longest: the longest phrase the model takes, in samples.
    init(_ models: Task<(any Recogniser, VadManager), Error>, longest: Int) {
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
    @concurrent private static func run(_ audio: SampleRing, _ models: Task<(any Recogniser, VadManager), Error>,
                                        _ longest: Int, _ show: AsyncStream<Dictation.Said>.Continuation) async throws -> String {
        defer { show.finish() }
        let (speech, vad) = try await models.value
        var ear = Ear(speech: speech, vad: vad, longest: longest, show: show)
        var chunk = [Float](repeating: 0, count: VadManager.chunkSize)
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
                Listening.keep(6_400, of: &before, adding: chunk)
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
/// it. The audio session and the engine start and stop on a queue of their
/// own, in turn (Turns): both wait on the system's audio, and take no time
/// from the main thread; a stop asked for as it starts wins, and the next
/// start waits for it. The engine is made and used on that queue only.
nonisolated final class Recording: @unchecked Sendable {
    static let format = AVAudioFormat(commonFormat: .pcmFormatFloat32, sampleRate: 16_000, channels: 1, interleaved: false)!
    private static let queue = DispatchQueue(label: "dev.gorex.recording", qos: .userInitiated)
    private lazy var engine = AVAudioEngine()
    /// Whether a start began, on the queue: a stop with none has nothing
    /// to stop.
    private var begun = false
    private let turns = Turns(on: Recording.queue)

    /// Starts the microphone, on its queue: heard takes what it hears, on
    /// the audio thread, until the next convert; done is told on the main
    /// actor whether it is on (false: a stop came first), or why it can't be.
    func start(_ heard: @escaping @Sendable (UnsafeBufferPointer<Float>) -> Void,
               done: @escaping @MainActor @Sendable (Result<Bool, any Error>) -> Void) {
        turns.start({ try self.begin(heard) }, done: done)
    }

    private func begin(_ heard: @escaping @Sendable (UnsafeBufferPointer<Float>) -> Void) throws {
        begun = true
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
        guard begun else { return }
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
