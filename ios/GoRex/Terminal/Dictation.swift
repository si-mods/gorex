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
    /// Speech cut into phrases, none longer than 30 s: SenseVoice takes
    /// 108 s at most, and cuts what is longer. 0.3 s before each keeps its
    /// first syllable.
    nonisolated private static let phrases = VadSegmentationConfig(minSpeechDuration: 0.3, maxSpeechDuration: 30, speechPadding: 0.3)

    /// The models on disk, whole.
    private(set) var installed = Dictation.present()
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

    /// What samples, 16 kHz mono, say: each phrase recognised, joined as
    /// spoken; empty for no speech. The models stay loaded a minute after.
    func transcribe(_ samples: [Float]) async throws -> String {
        idle?.cancel()
        defer {
            idle = Task {
                try? await Task.sleep(for: .seconds(60))
                if !Task.isCancelled { models = nil }
            }
        }
        if models == nil { models = Task { try await Self.load() } }
        let (speech, vad): (SenseVoiceManager, VadManager)
        do {
            (speech, vad) = try await models!.value
        } catch {
            models = nil
            throw error
        }
        var said: [String] = []
        for phrase in try await vad.segmentSpeechAudio(samples, config: Self.phrases) {
            let r = try await speech.transcribeDetailed(audio: phrase)
            if r.language != "nospeech" { said.append(r.text) }
        }
        return Self.joined(said)
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

/// The microphone while the mic is on: what it hears, as SenseVoice takes
/// it, 16 kHz mono floats. A call or a new route stops it; what was heard
/// stays.
final class Recording {
    nonisolated static let format = AVAudioFormat(commonFormat: .pcmFormatFloat32, sampleRate: 16_000, channels: 1, interleaved: false)!
    private let engine = AVAudioEngine()
    private let heard: OSAllocatedUnfairLock<Samples>

    /// Starts the microphone.
    init() throws {
        let audio = AVAudioSession.sharedInstance()
        try audio.setCategory(.record)
        try audio.setActive(true)
        let input = engine.inputNode
        let format = input.outputFormat(forBus: 0)
        guard let s = Samples(from: format) else {
            try? audio.setActive(false, options: .notifyOthersOnDeactivation)
            throw RexError(code: "dictation", message: String(localized: "This device has no microphone."))
        }
        let heard = OSAllocatedUnfairLock(uncheckedState: s)
        self.heard = heard
        input.installTap(onBus: 0, bufferSize: 4096, format: format) { @Sendable buffer, _ in
            heard.withLockUnchecked { $0.add(buffer) }
        }
        do {
            try engine.start()
        } catch {
            _ = stop()
            throw error
        }
    }

    /// Stops the microphone, and gives what it heard; others' sound comes
    /// back.
    func stop() -> [Float] {
        engine.stop()
        engine.inputNode.removeTap(onBus: 0)
        try? AVAudioSession.sharedInstance().setActive(false, options: .notifyOthersOnDeactivation)
        return heard.withLockUnchecked { $0.samples }
    }

    /// What was heard, converted from the microphone's format.
    nonisolated struct Samples {
        private let converter: AVAudioConverter
        private(set) var samples: [Float] = []

        /// Nil for a microphone that hears nothing, as a simulator's.
        init?(from source: AVAudioFormat) {
            guard source.sampleRate > 0, source.channelCount > 0,
                  let c = AVAudioConverter(from: source, to: Recording.format) else { return nil }
            // The first channel of several: the microphone's.
            if source.channelCount > 1 { c.channelMap = [0] }
            converter = c
        }

        mutating func add(_ buffer: AVAudioPCMBuffer) {
            let frames = Double(buffer.frameLength) * Recording.format.sampleRate / buffer.format.sampleRate
            guard let out = AVAudioPCMBuffer(pcmFormat: Recording.format, frameCapacity: AVAudioFrameCount(frames.rounded(.up)) + 16) else { return }
            // The converter asks until it has nothing more: the buffer once.
            var given = false
            _ = converter.convert(to: out, error: nil) { _, status in
                status.pointee = given ? .noDataNow : .haveData
                defer { given = true }
                return given ? nil : buffer
            }
            if let c = out.floatChannelData?[0] { samples.append(contentsOf: UnsafeBufferPointer(start: c, count: Int(out.frameLength))) }
        }
    }
}
