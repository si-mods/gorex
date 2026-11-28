import CoreML
import Foundation

/// Native Apple Core ML runner for SenseVoiceSmall (IOS-033), reusing
/// FbankFrontend and removing the FluidAudio / NemoTextProcessing dependency.
actor CoreMLSenseVoice: Recogniser {
    static let maxFrames = 1800
    /// Query tokens: [language(0=auto), 1, 2, textnorm(14=withitn)].
    private static let queryIDs: [Int32] = [0, 1, 2, 14]
    /// The CPU, on a device as on the simulator: `.all` compiles the model
    /// for the GPU at the first phrase of each load, which then shows
    /// nothing for 18 s on a Mac; on the CPU a phrase takes 0.1 s, in a
    /// fifth of the memory (IOS-033).
    static let computeUnits = MLComputeUnits.cpuOnly

    private let frontend = FbankFrontend()
    private let tokens: [String]
    private nonisolated(unsafe) let model: MLModel

    init(in dir: URL) throws {
        let tokData = try Data(contentsOf: dir.appending(path: "tokens.json"))
        self.tokens = try JSONDecoder().decode([String].self, from: tokData)
        let cfg = MLModelConfiguration()
        cfg.computeUnits = Self.computeUnits
        self.model = try MLModel(contentsOf: dir.appending(path: "SenseVoiceSmall.mlmodelc"), configuration: cfg)
    }

    func recognise(_ samples: [Float]) async throws -> String {
        let (features, rawFrames) = frontend.extract(samples)
        guard rawFrames > 0 else { return "" }
        let numFrames = min(rawFrames, Self.maxFrames)

        let speechArr = try MLMultiArray(
            shape: [1, NSNumber(value: numFrames), NSNumber(value: FbankFrontend.outputDim)],
            dataType: .float32
        )
        let byteCount = numFrames * FbankFrontend.outputDim * MemoryLayout<Float>.stride
        features.withUnsafeBytes { src in
            _ = memcpy(speechArr.dataPointer, src.baseAddress!, byteCount)
        }

        let queryArr = try MLMultiArray(shape: [1, 4], dataType: .int32)
        let qPtr = queryArr.dataPointer.assumingMemoryBound(to: Int32.self)
        for i in 0..<4 { qPtr[i] = Self.queryIDs[i] }

        let input = try MLDictionaryFeatureProvider(dictionary: [
            "speech": MLFeatureValue(multiArray: speechArr),
            "query_ids": MLFeatureValue(multiArray: queryArr),
        ])
        let out = try await model.prediction(from: input)
        guard let tokenArr = out.featureValue(for: "token_ids")?.multiArrayValue else {
            throw CocoaError(.fileReadCorruptFile)
        }

        let totalFrames = tokenArr.shape.last?.intValue ?? tokenArr.count
        let stride = tokenArr.strides.last?.intValue ?? 1
        let ptr = tokenArr.dataPointer.assumingMemoryBound(to: Int32.self)
        var ids = [Int]()
        ids.reserveCapacity(totalFrames)
        for i in 0..<totalFrames {
            ids.append(Int(ptr[i * stride]))
        }
        return Self.decodeCTC(ids, tokens: tokens)
    }

    /// Greedy CTC collapse (drop blank 0, collapse repeats), SentencePiece
    /// detokenize, return "" on <|nospeech|>, and strip <|...|> meta tags.
    nonisolated static func decodeCTC(_ ids: [Int], tokens: [String]) -> String {
        var prev = -1
        var raw = ""
        for id in ids {
            if id != 0 && id != prev {
                if id >= 0 && id < tokens.count {
                    raw += tokens[id]
                }
            }
            prev = id
        }
        if raw.contains("<|nospeech|>") { return "" }
        let cleaned = raw
            .replacingOccurrences(of: "\u{2581}", with: " ")
            .replacingOccurrences(of: "<\\|[^|]*\\|>", with: "", options: .regularExpression)
            .trimmingCharacters(in: .whitespacesAndNewlines)
        return cleaned
    }
}
