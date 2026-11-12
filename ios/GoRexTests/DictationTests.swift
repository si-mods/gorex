import AVFoundation
import XCTest

/// Dictation's phrases, joined as spoken.
nonisolated final class DictationTests: XCTestCase {
    /// SenseVoice is FluidAudio's; the others are pinned Hugging Face
    /// files, their size their sum. A phrase is cut under each model's
    /// limit: 29 s, and 20 for Fun-ASR-Nano.
    func testModels() {
        XCTAssertEqual(Dictation.Model.allCases, [.senseVoice, .funASRNano, .qwen3ASR])
        XCTAssertNil(Dictation.Model.senseVoice.repo)
        XCTAssertEqual(Dictation.Model.senseVoice.longest, 29 * 16_000)
        XCTAssertEqual(Dictation.Model.qwen3ASR.longest, 29 * 16_000)
        XCTAssertEqual(Dictation.Model.funASRNano.longest, 20 * 16_000)
        XCTAssertEqual(Dictation.Model.funASRNano.size, 1_009_605_061)
        XCTAssertEqual(Dictation.Model.qwen3ASR.size, 987_015_347)
        for m in [Dictation.Model.funASRNano, .qwen3ASR] {
            let repo = m.repo!
            XCTAssertEqual(repo.commit.count, 40, m.name)
            XCTAssertEqual(Set(repo.files.map(\.path)).count, repo.files.count, m.name)
        }
    }

    /// A model is whole with each file at its size: not with one missing,
    /// short, or still .partial.
    func testWholeTakesEachFileAtItsSize() throws {
        let dir = FileManager.default.temporaryDirectory.appending(path: UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: dir) }
        let files = [(path: "a.onnx", size: 3), (path: "t/vocab.json", size: 2)]
        XCTAssertFalse(Dictation.whole(files, in: dir))
        try FileManager.default.createDirectory(at: dir.appending(path: "t"), withIntermediateDirectories: true)
        try Data(count: 3).write(to: dir.appending(path: "a.onnx"))
        try Data(count: 2).write(to: dir.appending(path: "t/vocab.json.partial"))
        XCTAssertFalse(Dictation.whole(files, in: dir))
        try Data(count: 1).write(to: dir.appending(path: "t/vocab.json"))
        XCTAssertFalse(Dictation.whole(files, in: dir))
        try Data(count: 2).write(to: dir.appending(path: "t/vocab.json"))
        XCTAssertTrue(Dictation.whole(files, in: dir))
    }

    /// The model chosen stays chosen.
    @MainActor func testModelChosenIsSaved() {
        let d = Dictation.shared
        let was = d.model
        defer { d.model = was }
        d.model = .qwen3ASR
        XCTAssertEqual(UserDefaults.standard.string(forKey: "dictationModel"), "qwen3ASR")
        d.model = .senseVoice
        XCTAssertEqual(UserDefaults.standard.string(forKey: "dictationModel"), "senseVoice")
    }

    /// Fun-ASR-Nano and Qwen3-ASR recognise a clip through sherpa-onnx:
    /// the models in SHERPA_MODELS, laid out as the app keeps them, and the
    /// clip, 16 kHz mono, in DICTATION_WAV (TEST_RUNNER_… to xcodebuild).
    func testSherpaRecognises() async throws {
        let env = ProcessInfo.processInfo.environment
        guard let models = env["SHERPA_MODELS"], let wav = env["DICTATION_WAV"] else { throw XCTSkip("SHERPA_MODELS or DICTATION_WAV not set") }
        let f = try AVAudioFile(forReading: URL(filePath: wav))
        let b = AVAudioPCMBuffer(pcmFormat: f.processingFormat, frameCapacity: AVAudioFrameCount(f.length))!
        try f.read(into: b)
        XCTAssertEqual(f.processingFormat.sampleRate, 16_000)
        let samples = Array(UnsafeBufferPointer(start: b.floatChannelData![0], count: Int(b.frameLength)))
        for m in [Dictation.Model.funASRNano, .qwen3ASR] {
            let dir = URL(filePath: models).appending(path: m.repo!.name)
            XCTAssertTrue(Dictation.whole(m.repo!.files, in: dir), m.name)
            let start = ContinuousClock.now
            let sherpa = try Sherpa(m, in: dir)
            let loaded = ContinuousClock.now
            let text = await sherpa.recognise(samples)
            let done = ContinuousClock.now
            print("SHERPA \(m.name): load \(loaded - start), \(Double(samples.count) / 16_000) s in \(done - loaded), \(Self.footprint()) MB: «\(text)»")
            XCTAssertFalse(text.isEmpty, m.name)
        }
    }

    /// The process's memory, as jetsam counts it, in MB.
    private static func footprint() -> Int {
        var info = task_vm_info_data_t()
        var count = mach_msg_type_number_t(MemoryLayout<task_vm_info_data_t>.size / MemoryLayout<natural_t>.size)
        _ = withUnsafeMutablePointer(to: &info) { $0.withMemoryRebound(to: integer_t.self, capacity: Int(count)) { task_info(mach_task_self_, task_flavor_t(TASK_VM_INFO), $0, &count) } }
        return Int(info.phys_footprint) / 1_000_000
    }
    /// English phrases take a space; Chinese and Japanese, which SenseVoice
    /// may space, take none between them, and keep one beside Latin words.
    func testPhrasesJoinAsWritten() {
        XCTAssertEqual(Dictation.joined(["Hello there.", "How are you?"]), "Hello there. How are you?")
        XCTAssertEqual(Dictation.joined(["今天天气很好。", "我们出去吧。"]), "今天天气很好。我们出去吧。")
        XCTAssertEqual(Dictation.joined(["こ ん に ち は"]), "こんにちは")
        XCTAssertEqual(Dictation.joined(["我在用 Claude Code 写代码。"]), "我在用 Claude Code 写代码。")
        XCTAssertEqual(Dictation.joined([]), "")
        XCTAssertEqual(Dictation.joined(["", " "]), "")
    }

    /// What is said so far: the phrases for good, then the guess at the one
    /// open, written as one; what is for good begins it.
    func testSaidShowsTheOpenPhraseAfterThoseForGood() {
        var said = Dictation.Said()
        XCTAssertEqual(said.text, "")
        said.open = "Hello"
        XCTAssertEqual(said.text, "Hello")
        XCTAssertEqual(said.settled, "")
        said.open = ""
        said.phrases = ["Hello there."]
        XCTAssertEqual(said.text, "Hello there.")
        said.open = "How are"
        XCTAssertEqual(said.text, "Hello there. How are")
        XCTAssertEqual(said.settled, "Hello there.")
        said = Dictation.Said(phrases: ["今天天气很好。"], open: "我们出")
        XCTAssertEqual(said.text, "今天天气很好。我们出")
        XCTAssertEqual(said.settled, "今天天气很好。")
        said = Dictation.Said(phrases: ["我在用"], open: "Claude Code")
        XCTAssertEqual(said.text, "我在用 Claude Code")
        for s in [Dictation.Said(phrases: ["A.", "今天"], open: "好"), Dictation.Said(phrases: ["こ ん"], open: "に ち は")] {
            XCTAssertTrue(s.text.hasPrefix(s.settled), "\(s)")
        }
    }

    /// The level shown: -50 dB and below is none, -10 dB and above full;
    /// it rises at once and falls by half.
    func testLevelRisesAtOnceAndFallsByHalf() {
        XCTAssertEqual(Dictation.level(of: 0, after: 0), 0)
        XCTAssertEqual(Dictation.level(of: 0.001, after: 0), 0, accuracy: 0.001)
        XCTAssertEqual(Dictation.level(of: 0.01, after: 0), 0.25, accuracy: 0.001)
        XCTAssertEqual(Dictation.level(of: 0.1, after: 0), 0.75, accuracy: 0.001)
        XCTAssertEqual(Dictation.level(of: 1, after: 0), 1)
        XCTAssertEqual(Dictation.level(of: 0, after: 0.8), 0.4, accuracy: 0.001)
        XCTAssertEqual(Dictation.level(of: 0.1, after: 0.5), 0.75, accuracy: 0.001)
    }
}
