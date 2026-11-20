import AVFoundation
import FluidAudio
import UIKit
import XCTest
import os

/// Dictation's phrases, joined as spoken.
nonisolated final class DictationTests: XCTestCase {
    /// SenseVoice is FluidAudio's; the others are pinned Hugging Face
    /// files, their size their sum, and the same model as one tarball on
    /// sherpa-onnx's releases, whose files are not always the commit's. A
    /// phrase is cut under each model's limit: 29 s, and 20 for Fun-ASR-Nano.
    func testModels() {
        XCTAssertEqual(Dictation.Model.allCases, [.senseVoice, .funASRNano, .qwen3ASR])
        XCTAssertNil(Dictation.Model.senseVoice.repo)
        XCTAssertNil(Dictation.Model.senseVoice.release)
        XCTAssertEqual(Dictation.Model.senseVoice.longest, 29 * 16_000)
        XCTAssertEqual(Dictation.Model.qwen3ASR.longest, 29 * 16_000)
        XCTAssertEqual(Dictation.Model.funASRNano.longest, 20 * 16_000)
        XCTAssertEqual(Dictation.Model.funASRNano.size, 1_009_605_061)
        XCTAssertEqual(Dictation.Model.qwen3ASR.size, 987_015_347)
        XCTAssertEqual(Dictation.Model.funASRNano.release?.asset, "sherpa-onnx-funasr-nano-int8-2025-12-30.tar.bz2")
        XCTAssertEqual(Dictation.Model.qwen3ASR.release?.asset, "sherpa-onnx-qwen3-asr-0.6B-int8-2026-03-25.tar.bz2")
        for m in [Dictation.Model.funASRNano, .qwen3ASR] {
            let repo = m.repo!
            XCTAssertEqual(repo.commit.count, 40, m.name)
            XCTAssertEqual(Set(repo.files.map(\.path)).count, repo.files.count, m.name)
            // The release holds the same files as the commit, at its own sizes.
            XCTAssertEqual(Set(m.release!.files.map(\.path)), Set(repo.files.map(\.path)), m.name)
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

    /// A release's tarball is read as it comes: the files wanted written
    /// where they belong, .partial taking their names, the rest passed
    /// over, and a name too long for its header taken from the one before
    /// it.
    func testTarballHoldsTheFilesWanted() throws {
        let fm = FileManager.default
        let archive = fm.temporaryDirectory.appending(path: UUID().uuidString)
        let dir = fm.temporaryDirectory.appending(path: UUID().uuidString)
        defer {
            try? fm.removeItem(at: archive)
            try? fm.removeItem(at: dir)
        }
        let long = String(repeating: "l", count: 120) + ".txt"
        let tar = Self.tar([
            (name: "pkg", kind: UInt8(ascii: "5"), data: []),
            (name: "pkg/notes.txt", kind: 0, data: Array("hello".utf8)),
            (name: "pkg/a.onnx", kind: 0, data: [UInt8](repeating: 0xAB, count: 1_000)),
            (name: "pkg/\(long)", kind: 0, data: [0x01, 0x02, 0x03]),
        ])
        try Self.bzip2(tar).write(to: archive)

        try TarBz2.extract([(path: "a.onnx", size: 1_000), (path: long, size: 3)], from: archive, into: dir)
        XCTAssertEqual(try Data(contentsOf: dir.appending(path: "a.onnx")), Data(repeating: 0xAB, count: 1_000))
        XCTAssertEqual(try Data(contentsOf: dir.appending(path: long)), Data([0x01, 0x02, 0x03]))
        XCTAssertFalse(fm.fileExists(atPath: dir.appending(path: "notes.txt").path))
        XCTAssertEqual(try fm.contentsOfDirectory(atPath: dir.path).filter { $0.hasSuffix(".partial") }, [])

        // A size that is not the archive's fails, rather than a file short.
        XCTAssertThrowsError(try TarBz2.extract([(path: "a.onnx", size: 999)], from: archive,
                                                into: fm.temporaryDirectory.appending(path: UUID().uuidString)))
    }

    /// A tar of the entries given, as tar writes it, a long name as GNU
    /// does.
    private static func tar(_ entries: [(name: String, kind: UInt8, data: [UInt8])]) -> [UInt8] {
        var out: [UInt8] = []
        for e in entries {
            var name = e.name
            let bytes = Array(name.utf8)
            if bytes.count > 100 {
                out += header("././@LongLink", bytes.count, UInt8(ascii: "L"))
                out += bytes + [UInt8](repeating: 0, count: (512 - bytes.count % 512) % 512)
                name = String(e.name.suffix(100))
            }
            out += header(name, e.data.count, e.kind)
            out += e.data + [UInt8](repeating: 0, count: (512 - e.data.count % 512) % 512)
        }
        return out + [UInt8](repeating: 0, count: 1_024)
    }

    private static func header(_ name: String, _ size: Int, _ kind: UInt8) -> [UInt8] {
        var h = [UInt8](repeating: 0, count: 512)
        func put(_ text: String, _ at: Int) {
            for (i, b) in Array(text.utf8).enumerated() where at + i < h.count { h[at + i] = b }
        }
        put(name, 0)
        put("0000644", 100)
        put("0000000", 108)
        put("0000000", 116)
        put(String(format: "%011o", size), 124)
        put("00000000000", 136)
        h[156] = kind
        put("ustar", 257)
        put("00", 263)
        for i in 148..<156 { h[i] = 32 }
        put(String(format: "%06o", h.reduce(0) { $0 + Int($1) }) + "\0 ", 148)
        return h
    }

    /// The archive as bzip2 has it.
    private static func bzip2(_ bytes: [UInt8]) throws -> Data {
        var out = [UInt8](repeating: 0, count: bytes.count + bytes.count / 50 + 1_024)
        var length = UInt32(out.count)
        let status = bytes.withUnsafeBufferPointer { source in
            out.withUnsafeMutableBufferPointer { dest in
                BZ2_bzBuffToBuffCompress(dest.baseAddress!.withMemoryRebound(to: CChar.self, capacity: dest.count) { $0 },
                                         &length,
                                         UnsafeMutablePointer(mutating: source.baseAddress!.withMemoryRebound(to: CChar.self, capacity: source.count) { $0 }),
                                         UInt32(bytes.count), 9, 0, 0)
            }
        }
        XCTAssertEqual(status, BZ_OK)
        return Data(out.prefix(Int(length)))
    }

    /// A release's tarball, as k2-fsa's `asr-models` serves it, is read by
    /// the app's reader whole: each of its model's files at the release's
    /// size. The archive is DICTATION_ARCHIVE, and the release's model
    /// DICTATION_MODEL, its name as Model has it (TEST_RUNNER_… to
    /// xcodebuild): ~840 MB, so not with every test run.
    func testTarballUnpacksARelease() throws {
        let env = ProcessInfo.processInfo.environment
        guard let archive = env["DICTATION_ARCHIVE"], let model = env["DICTATION_MODEL"].flatMap(Dictation.Model.init(rawValue:)),
              let release = model.release
        else { throw XCTSkip("DICTATION_ARCHIVE or DICTATION_MODEL not set") }
        let dir = FileManager.default.temporaryDirectory.appending(path: UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: dir) }
        let start = ContinuousClock.now
        try TarBz2.extract(release.files, from: URL(filePath: archive), into: dir)
        print("TARBALL \(release.asset): \(ContinuousClock.now - start)")
        XCTAssertTrue(Dictation.whole(release.files, in: dir), model.name)
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

    /// Memory running short unloads the model at once, as it would be a
    /// minute after the mic went off.
    @MainActor func testMemoryWarningUnloadsTheModel() {
        let d = Dictation.shared
        let was = (d.models, d.listenings)
        defer { (d.models, d.listenings) = was }
        d.models = Task { throw CancellationError() }
        d.listenings = 1
        XCTAssertTrue(d.loaded)
        NotificationCenter.default.post(name: UIApplication.didReceiveMemoryWarningNotification, object: nil)
        XCTAssertFalse(d.loaded)
    }

    /// The app going to the background unloads the model, but not while a
    /// listening goes on.
    @MainActor func testBackgroundUnloadsTheModelWithTheMicOff() {
        let d = Dictation.shared
        let was = (d.models, d.listenings)
        defer { (d.models, d.listenings) = was }
        d.models = Task { throw CancellationError() }
        d.listenings = 1
        NotificationCenter.default.post(name: UIApplication.didEnterBackgroundNotification, object: nil)
        XCTAssertTrue(d.loaded)
        d.listenings = 0
        NotificationCenter.default.post(name: UIApplication.didEnterBackgroundNotification, object: nil)
        XCTAssertFalse(d.loaded)
    }

    /// What the mic hears waits for the model up to the backlog, and no
    /// more; none once the listening is over.
    func testAudioWaitingForTheModelIsBounded() {
        // A load that ends only with the test: the audio waits until then.
        let (gate, open) = AsyncStream.makeStream(of: Void.self)
        let models = Task<(any Recogniser, VadManager), Error> {
            for await _ in gate {}
            throw CancellationError()
        }
        defer { open.finish() }
        let listening = Listening(models, longest: 16_000)
        XCTAssertEqual(Listening.backlog, 60 * 16_000)
        let second = [Float](repeating: 0, count: 16_000)
        for i in 0..<60 { XCTAssertTrue(listening.add(second), "\(i)") }
        XCTAssertFalse(listening.add([0]))
        listening.cancel()
        XCTAssertFalse(listening.add([]))
    }

    /// The ring gives the samples in the order written, across its end, and
    /// holds no more than its capacity: a write that does not fit is
    /// dropped whole, and none is taken after finish, what was written
    /// staying to be read.
    func testRingKeepsOrderAcrossItsEndAndItsBound() {
        let ring = SampleRing(capacity: 5)
        func write(_ s: [Float]) -> Bool { s.withUnsafeBufferPointer { ring.write($0) } }
        func read(_ n: Int) -> [Float] {
            var out = [Float](repeating: -1, count: n)
            let got = out.withUnsafeMutableBufferPointer { ring.read(into: $0) }
            return Array(out.prefix(got))
        }
        XCTAssertTrue(write([1, 2, 3]))
        XCTAssertEqual(read(2), [1, 2])
        // 4 to 7 wrap past the end: two at its last places, two at its first.
        XCTAssertTrue(write([4, 5, 6, 7]))
        XCTAssertEqual(ring.count, 5)
        XCTAssertFalse(write([8]))
        XCTAssertEqual(read(10), [3, 4, 5, 6, 7])
        XCTAssertEqual(read(1), [])
        XCTAssertTrue(write([8, 9, 10, 11, 12]))
        XCTAssertFalse(write([1, 2, 3, 4, 5, 6]))
        XCTAssertEqual(read(3), [8, 9, 10])
        // Three places free, four asked for: none written.
        XCTAssertFalse(write([13, 14, 15, 16]))
        XCTAssertTrue(write([13, 14, 15]))
        ring.finish()
        XCTAssertTrue(ring.finished)
        XCTAssertFalse(write([16]))
        XCTAssertEqual(read(10), [11, 12, 13, 14, 15])
    }

    /// A writer on one thread and a reader on another: every sample comes
    /// through once, in order, the ring a fraction of what passes.
    func testRingPassesSamplesBetweenThreadsInOrder() {
        let ring = SampleRing(capacity: 1_000)
        let total = 200_000
        DispatchQueue.global().async {
            var next = 0
            var piece = [Float](repeating: 0, count: 137)
            while next < total {
                let n = min(piece.count, total - next)
                for i in 0..<n { piece[i] = Float(next + i) }
                if piece.withUnsafeBufferPointer({ ring.write(UnsafeBufferPointer(rebasing: $0[..<n])) }) { next += n }
            }
            ring.finish()
        }
        var got = 0
        var out = [Float](repeating: 0, count: 300)
        var inOrder = true
        while true {
            let over = ring.finished
            let n = out.withUnsafeMutableBufferPointer { ring.read(into: $0) }
            for i in 0..<n where out[i] != Float(got + i) { inOrder = false }
            got += n
            if n == 0, over { break }
        }
        XCTAssertTrue(inOrder)
        XCTAssertEqual(got, total)
    }

    /// The level shown is the newest the microphone gave, taken once.
    func testMeterGivesTheNewestLevelOnce() {
        let meter = Meter()
        var count: UInt64 = 0
        XCTAssertNil(meter.take(after: &count))
        meter.set(0.1)
        meter.set(0.2)
        XCTAssertEqual(meter.take(after: &count), 0.2)
        XCTAssertNil(meter.take(after: &count))
        meter.set(0)
        XCTAssertEqual(meter.take(after: &count), 0)
        XCTAssertNil(meter.take(after: &count))
    }

    /// A stop asked for while the microphone starts wins: it runs after
    /// the start, which is told it ran.
    func testStopDuringStartRunsAfterIt() {
        let queue = DispatchQueue(label: "test.turns")
        let turns = Turns(on: queue)
        let log = OSAllocatedUnfairLock(initialState: [String]())
        let starting = DispatchSemaphore(value: 0)
        let go = DispatchSemaphore(value: 0)
        let done = expectation(description: "done")
        turns.start({
            starting.signal()
            go.wait()
            log.withLock { $0.append("start") }
        }, done: { r in
            XCTAssertEqual(try? r.get(), true)
            done.fulfill()
        })
        starting.wait()
        let stopped = expectation(description: "stopped")
        turns.stop {
            log.withLock { $0.append("stop") }
            stopped.fulfill()
        }
        go.signal()
        wait(for: [done, stopped], timeout: 10)
        XCTAssertEqual(log.withLock { $0 }, ["start", "stop"])
    }

    /// A stop asked for before the start begins passes the start over; a
    /// start that throws tells why.
    func testStopBeforeStartPassesItOver() {
        let queue = DispatchQueue(label: "test.turns")
        let turns = Turns(on: queue)
        let log = OSAllocatedUnfairLock(initialState: [String]())
        // The queue busy until the stop is asked for.
        let busy = DispatchSemaphore(value: 0)
        queue.async { busy.wait() }
        let done = expectation(description: "done")
        turns.start({ log.withLock { $0.append("start") } }, done: { r in
            XCTAssertEqual(try? r.get(), false)
            done.fulfill()
        })
        let stopped = expectation(description: "stopped")
        turns.stop {
            log.withLock { $0.append("stop") }
            stopped.fulfill()
        }
        busy.signal()
        wait(for: [done, stopped], timeout: 10)
        XCTAssertEqual(log.withLock { $0 }, ["stop"])

        let failed = expectation(description: "failed")
        Turns(on: queue).start({ throw CancellationError() }, done: { r in
            if case .failure(let e) = r { XCTAssertTrue(e is CancellationError) } else { XCTFail("\(r)") }
            failed.fulfill()
        })
        wait(for: [failed], timeout: 10)
    }

    /// The scan off the main actor finds what the scan on it did, and the
    /// mic's wait for it gives that too.
    @MainActor func testScanFindsTheModelsOnDisk() async {
        let d = Dictation.shared
        let found = await d.installedModels()
        XCTAssertTrue(d.scanned)
        XCTAssertEqual(found, Set(Dictation.Model.allCases.filter(Dictation.present)))
        XCTAssertEqual(d.installed, found)
        let again = await Dictation.onDisk()
        XCTAssertEqual(again, found)
    }

    /// What came before speech keeps its last samples, in order.
    func testKeepHoldsTheLastSamples() {
        var kept: [Float] = []
        Listening.keep(4, of: &kept, adding: [1, 2, 3])
        XCTAssertEqual(kept, [1, 2, 3])
        Listening.keep(4, of: &kept, adding: [4, 5])
        XCTAssertEqual(kept, [2, 3, 4, 5])
        Listening.keep(4, of: &kept, adding: [6, 7, 8, 9, 10])
        XCTAssertEqual(kept, [7, 8, 9, 10])
    }

    /// The microphone's 48 kHz converts to 16 kHz in one buffer, made once
    /// and used again for each buffer the mic gives.
    func testConverterUsesOneBuffer() throws {
        let source = try XCTUnwrap(AVAudioFormat(commonFormat: .pcmFormatFloat32, sampleRate: 48_000, channels: 1, interleaved: false))
        var c = try XCTUnwrap(Recording.Converter(from: source))
        let input = try XCTUnwrap(AVAudioPCMBuffer(pcmFormat: source, frameCapacity: 4_800))
        input.frameLength = 4_800
        for i in 0..<4_800 { input.floatChannelData![0][i] = sin(Float(i) * 0.05) }
        var total = c.convert(input).count
        let out = try XCTUnwrap(c.out)
        for _ in 0..<4 {
            total += c.convert(input).count
            XCTAssertTrue(c.out === out)
        }
        // A converter keeps back a few samples it has not yet filtered.
        XCTAssertEqual(Double(total), 8_000, accuracy: 200)
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
            // The commit's files, or the release's build of them.
            XCTAssertTrue(Dictation.whole(m.repo!.files, in: dir) || Dictation.whole(m.release!.files, in: dir), m.name)
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
