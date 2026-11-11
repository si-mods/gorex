import AVFoundation
import XCTest

/// Voice: a server lists it in its welcome, and what the microphone hears
/// goes to it as rec's PCM, signed 16-bit little-endian at 16 kHz, mono, in
/// messages of 100 ms.
nonisolated final class VoiceTests: XCTestCase {
    /// Only a server whose welcome lists gorex.voice takes voice, so an
    /// older one shows no mic key.
    @MainActor func testVoiceFollowsTheWelcome() async throws {
        let rex = try FakeRex()
        defer { rex.stop() }
        rex.respond = { method, _ in method == "session.bootstrap" ? ["attach_all": ["sessions": []]] : [:] }
        for (features, can) in [([], false), (["gorex.upload", "gorex.voice"], true)] {
            rex.features = features
            let h = Host(HostConfig(name: "test", url: try await rex.url()))
            XCTAssertFalse(h.canVoice)
            h.connect()
            await until("online") { h.status == .online }
            XCTAssertEqual(h.canVoice, can, "\(features)")
            h.stop()
        }
    }

    /// A buffer of samples in a format, mono.
    private func buffer(_ format: AVAudioFormat, _ samples: [Float]) throws -> AVAudioPCMBuffer {
        let b = try XCTUnwrap(AVAudioPCMBuffer(pcmFormat: format, frameCapacity: AVAudioFrameCount(samples.count)))
        b.frameLength = AVAudioFrameCount(samples.count)
        for (i, s) in samples.enumerated() {
            if let f = b.floatChannelData { f[0][i] = s } else { b.int16ChannelData![0][i] = Int16(s) }
        }
        return b
    }

    /// Audio in rec's format goes as it is, cut into messages of 3200
    /// bytes; what is short of one waits for the end.
    func testPCMCutsMessages() throws {
        var pcm = try XCTUnwrap(Voice.PCM(from: Voice.format))
        let samples = (0..<5000).map { Float($0 % 2000 - 1000) }
        var sent: [Data] = []
        for part in stride(from: 0, to: samples.count, by: 1000) {
            sent += pcm.add(try buffer(Voice.format, Array(samples[part..<part + 1000])))
        }
        XCTAssertEqual(sent.map(\.count), [3200, 3200, 3200])
        let rest = pcm.rest()
        XCTAssertEqual(rest.count, 10_000 - 9600)
        XCTAssertEqual(pcm.rest(), Data())
        let all = (sent + [rest]).reduce(Data(), +)
        let little = samples.flatMap { s in withUnsafeBytes(of: Int16(s).littleEndian, Array.init) }
        XCTAssertEqual(all, Data(little))
    }

    /// A microphone's 48 kHz floats come down to 16 kHz, 16-bit: a second
    /// of a tone is a second of samples, near as loud.
    func testPCMConvertsTheMicrophone() throws {
        let mic = try XCTUnwrap(AVAudioFormat(standardFormatWithSampleRate: 48_000, channels: 1))
        var pcm = try XCTUnwrap(Voice.PCM(from: mic))
        let tone = (0..<48_000).map { Float(0.5 * sin(2 * Double.pi * 440 * Double($0) / 48_000)) }
        var sent: [Data] = []
        for part in stride(from: 0, to: tone.count, by: 4800) {
            sent += pcm.add(try buffer(mic, Array(tone[part..<part + 4800])))
        }
        XCTAssertTrue(sent.allSatisfy { $0.count == Voice.chunk })
        let all = (sent + [pcm.rest()]).reduce(Data(), +)
        // The converter holds back a few samples of the last buffer.
        XCTAssertEqual(Double(all.count), 32_000, accuracy: 200)
        let peak = all.withUnsafeBytes { $0.bindMemory(to: Int16.self).map { abs(Int(Int16(littleEndian: $0))) }.max() ?? 0 }
        XCTAssertEqual(Double(peak), 0.5 * 32_767, accuracy: 1000)
    }

    /// A simulator without a microphone gives a format of no sound, which
    /// streams nothing.
    func testPCMNeedsSound() {
        let none = AVAudioFormat(standardFormatWithSampleRate: 0, channels: 1)
        XCTAssertNil(none.flatMap(Voice.PCM.init(from:)))
    }
}
