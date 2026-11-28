import CoreML
import Foundation

/// Native Apple Core ML runner for Fun-ASR-Nano (IOS-031).
/// Replaces SherpaOnnxC with three Core ML packages:
///   1. EncoderAdaptor.mlmodelc  — SenseVoiceEncoderSmall + Transformer adaptor
///   2. TokenEmbedding.mlmodelc  — Qwen3-0.6B token embedding lookup (CPU-only gather)
///   3. Qwen3Decoder.mlmodelc    — 28-layer Qwen3-0.6B with iOS 18+ MLState KV-cache
///
/// All three load once, on the CPU, and stay loaded while the recogniser
/// is: each phrase then takes a fresh KV-cache only (IOS-031, Latency).
actor CoreMLFunASR: Recogniser {
    static let maxSeqLen = 512
    static let maxNewTokens = 256
    static let hiddenSize = 1024

    /// The CPU, on a device as on the simulator. `.all` compiles the
    /// encoder and decoder for the GPU at their first prediction, and again
    /// for each model loaded: 20 to 40 s a phrase on a Mac, and the memory
    /// iOS ended the app for (#1244). On the CPU their weights stay mapped
    /// from disk, and a token takes about as long as on the Neural Engine.
    static let computeUnits = MLComputeUnits.cpuOnly

    private let frontend = FbankFrontend()
    private let tokenizer: QwenTokenizer
    private nonisolated(unsafe) let encoder: MLModel
    private nonisolated(unsafe) let embedding: MLModel
    private nonisolated(unsafe) let decoder: MLModel

    init(in dir: URL) throws {
        self.tokenizer = try QwenTokenizer(
            vocabURL: dir.appending(path: "vocab.json"),
            promptURL: dir.appending(path: "prompt_ids.json")
        )
        let cfg = MLModelConfiguration()
        cfg.computeUnits = Self.computeUnits
        self.encoder = try MLModel(contentsOf: dir.appending(path: "EncoderAdaptor.mlmodelc"), configuration: cfg)
        self.embedding = try MLModel(contentsOf: dir.appending(path: "TokenEmbedding.mlmodelc"), configuration: cfg)
        self.decoder = try MLModel(contentsOf: dir.appending(path: "Qwen3Decoder.mlmodelc"), configuration: cfg)
    }

    /// Calls `MLModel.makeState()` inside an Objective-C `@try/@catch` so an
    /// `NSException` from Core ML's `MLE5Engine` becomes a catchable Swift error.
    nonisolated static func makeState(for model: MLModel) throws -> MLState {
        var state: MLState?
        var nsError: NSError?
        guard GoRexCatchObjCException({ state = model.makeState() }, &nsError) else {
            throw nsError ?? CocoaError(.coderInvalidValue)
        }
        guard let state else {
            throw CocoaError(.coderInvalidValue)
        }
        return state
    }

    func recognise(_ samples: [Float]) async throws -> String {
        let (features, numFrames) = frontend.extract(samples)
        guard numFrames > 0 else { return "" }

        let (prefillEmbeds, contextLen) = try await buildPrefillEmbeddings(features: features, numFrames: numFrames)
        guard contextLen > 0, contextLen < Self.maxSeqLen else { return "" }

        // A fresh KV-cache for each phrase; makeState raises an
        // Objective-C exception, caught, when Core ML cannot allocate it.
        let state = try Self.makeState(for: decoder)
        let eosSet = Set(tokenizer.prompt.eosIDs)
        var nextToken = try decodeStep(embeds: prefillEmbeds, startPos: 0, seqLen: contextLen, state: state)

        // At most a token for each frame of audio, 16 a second, past any
        // speech: a decode looping on noise ends as soon as the phrase would.
        let audioLen = contextLen - tokenizer.prompt.idsBefore.count - tokenizer.prompt.idsAfter.count
        let most = min(Self.maxNewTokens, max(16, audioLen))
        var generated: [Int] = []
        generated.reserveCapacity(64)
        var pos = contextLen

        while generated.count < most && pos < Self.maxSeqLen {
            if eosSet.contains(nextToken) { break }
            // A listening cancelled stops here, not at the phrase's end.
            try Task.checkCancellation()
            generated.append(nextToken)
            let tokEmbed = try await embedTokens([nextToken])
            nextToken = try decodeStep(embeds: tokEmbed, startPos: pos, seqLen: 1, state: state)
            pos += 1
        }

        return tokenizer.decode(generated)
    }

    private func buildPrefillEmbeddings(features: [Float], numFrames: Int) async throws -> (MLMultiArray, Int) {
        let audioEmbeds = try runEncoder(features: features, numFrames: numFrames)
        let audioLen = audioEmbeds.shape[1].intValue

        let beforeEmbeds = try await embedTokens(tokenizer.prompt.idsBefore)
        let afterEmbeds = try await embedTokens(tokenizer.prompt.idsAfter)

        let beforeLen = tokenizer.prompt.idsBefore.count
        let afterLen = tokenizer.prompt.idsAfter.count
        let maxAudioLen = max(0, Self.maxSeqLen - beforeLen - afterLen - 16)
        let usedAudioLen = min(audioLen, maxAudioLen)
        let contextLen = beforeLen + usedAudioLen + afterLen
        guard contextLen > 0, contextLen < Self.maxSeqLen else {
            return (try MLMultiArray(shape: [1, 1, NSNumber(value: Self.hiddenSize)], dataType: .float16), 0)
        }

        // Concatenate [beforeEmbeds, audioEmbeds[:, :usedAudioLen, :], afterEmbeds] -> Float16 [1, contextLen, 1024].
        let prefillEmbeds = try MLMultiArray(shape: [1, NSNumber(value: contextLen), NSNumber(value: Self.hiddenSize)], dataType: .float16)
        copyFloat16Rows(from: beforeEmbeds, count: beforeLen, to: prefillEmbeds, dstRow: 0)
        copyFloat16Rows(from: audioEmbeds, count: usedAudioLen, to: prefillEmbeds, dstRow: beforeLen)
        copyFloat16Rows(from: afterEmbeds, count: afterLen, to: prefillEmbeds, dstRow: beforeLen + usedAudioLen)
        return (prefillEmbeds, contextLen)
    }

    private func runEncoder(features: [Float], numFrames: Int) throws -> MLMultiArray {
        let speechArr = try MLMultiArray(shape: [1, NSNumber(value: numFrames), NSNumber(value: FbankFrontend.outputDim)], dataType: .float32)
        features.withUnsafeBytes { src in
            _ = memcpy(speechArr.dataPointer, src.baseAddress!, src.count)
        }
        let encIn = try MLDictionaryFeatureProvider(dictionary: ["speech": MLFeatureValue(multiArray: speechArr)])
        let encOut = try encoder.prediction(from: encIn)
        guard let audioEmbeds = encOut.featureValue(for: "audio_embeds")?.multiArrayValue else {
            throw CocoaError(.fileReadCorruptFile)
        }
        return audioEmbeds
    }

    private func embedTokens(_ ids: [Int]) async throws -> MLMultiArray {
        let arr = try MLMultiArray(shape: [1, NSNumber(value: ids.count)], dataType: .int32)
        let ptr = arr.dataPointer.assumingMemoryBound(to: Int32.self)
        for (i, id) in ids.enumerated() {
            ptr[i] = Int32(id)
        }
        let input = try MLDictionaryFeatureProvider(dictionary: ["token_ids": MLFeatureValue(multiArray: arr)])
        let out = try await embedding.prediction(from: input)
        guard let embeds = out.featureValue(for: "embeds")?.multiArrayValue else {
            throw CocoaError(.fileReadCorruptFile)
        }
        return embeds
    }

    private func decodeStep(embeds: MLMultiArray, startPos: Int, seqLen: Int, state: MLState) throws -> Int {
        let posArr = try MLMultiArray(shape: [1, NSNumber(value: seqLen)], dataType: .int32)
        let posPtr = posArr.dataPointer.assumingMemoryBound(to: Int32.self)
        for i in 0..<seqLen {
            posPtr[i] = Int32(startPos + i)
        }

        // Causal mask: [1, 1, seqLen, maxSeqLen], 0.0 for j <= startPos + i, -1e4 otherwise.
        let maskArr = try MLMultiArray(shape: [1, 1, NSNumber(value: seqLen), NSNumber(value: Self.maxSeqLen)], dataType: .float16)
        let maskPtr = maskArr.dataPointer.assumingMemoryBound(to: Float16.self)
        let negInf = Float16(-10000.0)
        let zero = Float16(0.0)
        for i in 0..<seqLen {
            let limit = startPos + i
            let row = i * Self.maxSeqLen
            for j in 0..<Self.maxSeqLen {
                maskPtr[row + j] = j <= limit ? zero : negInf
            }
        }

        let input = try MLDictionaryFeatureProvider(dictionary: [
            "inputs_embeds": MLFeatureValue(multiArray: embeds),
            "position_ids": MLFeatureValue(multiArray: posArr),
            "causal_mask": MLFeatureValue(multiArray: maskArr),
        ])
        let out = try decoder.prediction(from: input, using: state)
        guard let logits = out.featureValue(for: "logits")?.multiArrayValue else {
            throw CocoaError(.fileReadCorruptFile)
        }
        return argmax(logits)
    }

    private func copyFloat16Rows(from src: MLMultiArray, count: Int, to dst: MLMultiArray, dstRow: Int) {
        let rowElems = Self.hiddenSize
        let dstPtr = dst.dataPointer.assumingMemoryBound(to: Float16.self).advanced(by: dstRow * rowElems)
        let rowStride = src.strides.count >= 2 ? src.strides[1].intValue : rowElems
        let colStride = src.strides.count >= 3 ? src.strides[2].intValue : 1
        let contiguous = rowStride == rowElems && colStride == 1
        if src.dataType == .float16 {
            let srcPtr = src.dataPointer.assumingMemoryBound(to: Float16.self)
            if contiguous {
                memcpy(dstPtr, srcPtr, count * rowElems * MemoryLayout<Float16>.stride)
            } else {
                for r in 0..<count {
                    let base = r * rowStride
                    let outBase = r * rowElems
                    for c in 0..<rowElems {
                        dstPtr[outBase + c] = srcPtr[base + c * colStride]
                    }
                }
            }
        } else {
            let srcPtr = src.dataPointer.assumingMemoryBound(to: Float.self)
            if contiguous {
                let total = count * rowElems
                for i in 0..<total {
                    dstPtr[i] = Float16(srcPtr[i])
                }
            } else {
                for r in 0..<count {
                    let base = r * rowStride
                    let outBase = r * rowElems
                    for c in 0..<rowElems {
                        dstPtr[outBase + c] = Float16(srcPtr[base + c * colStride])
                    }
                }
            }
        }
    }

    private func argmax(_ logits: MLMultiArray) -> Int {
        let count = logits.shape.last?.intValue ?? logits.count
        let stride = logits.strides.last?.intValue ?? 1
        var bestIdx = 0
        var bestVal: Float = -.infinity
        if logits.dataType == .float16 {
            let ptr = logits.dataPointer.assumingMemoryBound(to: Float16.self)
            for i in 0..<count {
                let v = Float(ptr[i * stride])
                if v.isFinite && v > bestVal {
                    bestVal = v
                    bestIdx = i
                }
            }
        } else {
            let ptr = logits.dataPointer.assumingMemoryBound(to: Float.self)
            for i in 0..<count {
                let v = ptr[i * stride]
                if v.isFinite && v > bestVal {
                    bestVal = v
                    bestIdx = i
                }
            }
        }
        return bestIdx
    }
}
