import CoreML
import Foundation
import os

/// Native Apple Core ML runner for Fun-ASR-Nano (IOS-031).
/// Replaces SherpaOnnxC with three Core ML packages running on ANE/GPU:
///   1. EncoderAdaptor.mlmodelc  — SenseVoiceEncoderSmall + Transformer adaptor
///   2. TokenEmbedding.mlmodelc  — Qwen3-0.6B token embedding lookup
///   3. Qwen3Decoder.mlmodelc    — 28-layer Qwen3-0.6B with iOS 18+ MLState KV-cache
///
/// Audio features are computed in Swift via Accelerate (FbankFrontend), and
/// generated token IDs are decoded via QwenTokenizer.
actor CoreMLFunASR: Recogniser {
    static let maxSeqLen = 512
    static let maxNewTokens = 256
    static let hiddenSize = 1024

    private static let log = Logger(subsystem: "dev.gorex.ios", category: "dictation")

    nonisolated static var preferredComputeUnits: [MLComputeUnits] {
        #if targetEnvironment(simulator)
        [.cpuOnly]
        #else
        [.all, .cpuAndGPU, .cpuOnly]
        #endif
    }

    private let frontend = FbankFrontend()
    private let tokenizer: QwenTokenizer
    private let decoderURL: URL
    // MLModel is thread-safe for prediction but not annotated Sendable in the iOS SDK;
    // actor isolation guarantees only one prediction uses each model at a time.
    private nonisolated(unsafe) let encoder: MLModel
    private nonisolated(unsafe) let embedding: MLModel
    private nonisolated(unsafe) var decoder: MLModel
    private var decoderUnitsIndex: Int

    init(in dir: URL) throws {
        self.tokenizer = try QwenTokenizer(
            vocabURL: dir.appending(path: "vocab.json"),
            promptURL: dir.appending(path: "prompt_ids.json")
        )
        let candidates = Self.preferredComputeUnits
        self.encoder = try Self.loadModel(at: dir.appending(path: "EncoderAdaptor.mlmodelc"), candidates: candidates).model
        self.embedding = try Self.loadModel(at: dir.appending(path: "TokenEmbedding.mlmodelc"), candidates: candidates).model
        self.decoderURL = dir.appending(path: "Qwen3Decoder.mlmodelc")
        // Validate makeState() at load time: on iOS, MLE5Engine.newStateWithClientBuffers:
        // can raise an Objective-C NSException under .all (ANE); catching it lets us fall
        // back to .cpuAndGPU or .cpuOnly instead of aborting with SIGABRT.
        let loadedDecoder = try Self.loadModel(at: decoderURL, candidates: candidates) { model in
            _ = try Self.makeState(for: model)
        }
        self.decoder = loadedDecoder.model
        self.decoderUnitsIndex = loadedDecoder.index
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

    /// Tries each `MLComputeUnits` candidate in order until `load` succeeds.
    nonisolated static func loadWithFallback<T>(
        candidates: [MLComputeUnits],
        load: (MLComputeUnits) throws -> T
    ) throws -> (value: T, index: Int) {
        var lastError: Error?
        for (index, units) in candidates.enumerated() {
            do {
                return (try load(units), index)
            } catch {
                lastError = error
                log.warning("CoreMLFunASR computeUnits=\(units.rawValue) failed: \(error.localizedDescription, privacy: .public)")
            }
        }
        throw lastError ?? CocoaError(.fileReadUnknown)
    }

    private nonisolated static func loadModel(
        at url: URL,
        candidates: [MLComputeUnits],
        validate: ((MLModel) throws -> Void)? = nil
    ) throws -> (model: MLModel, index: Int) {
        let loaded = try loadWithFallback(candidates: candidates) { units -> MLModel in
            let cfg = MLModelConfiguration()
            cfg.computeUnits = units
            let model = try MLModel(contentsOf: url, configuration: cfg)
            try validate?(model)
            return model
        }
        return (loaded.value, loaded.index)
    }

    private func makeDecoderState() throws -> MLState {
        do {
            return try Self.makeState(for: decoder)
        } catch {
            let candidates = Self.preferredComputeUnits
            let nextIndex = decoderUnitsIndex + 1
            guard nextIndex < candidates.count else { throw error }
            Self.log.warning("CoreMLFunASR decoder makeState() failed at runtime; falling back: \(error.localizedDescription, privacy: .public)")
            let remaining = Array(candidates[nextIndex...])
            let loaded = try Self.loadWithFallback(candidates: remaining) { units -> (MLModel, MLState) in
                let cfg = MLModelConfiguration()
                cfg.computeUnits = units
                let model = try MLModel(contentsOf: decoderURL, configuration: cfg)
                let state = try Self.makeState(for: model)
                return (model, state)
            }
            self.decoder = loaded.value.0
            self.decoderUnitsIndex = nextIndex + loaded.index
            return loaded.value.1
        }
    }

    func recognise(_ samples: [Float]) async throws -> String {
        let (features, numFrames) = frontend.extract(samples)
        guard numFrames > 0 else { return "" }

        // 1. Run EncoderAdaptor: [1, numFrames, 560] -> [1, numFrames, 1024]
        let speechArr = try MLMultiArray(shape: [1, NSNumber(value: numFrames), NSNumber(value: FbankFrontend.outputDim)], dataType: .float32)
        features.withUnsafeBytes { src in
            _ = memcpy(speechArr.dataPointer, src.baseAddress!, src.count)
        }
        let encIn = try MLDictionaryFeatureProvider(dictionary: ["speech": MLFeatureValue(multiArray: speechArr)])
        let encOut = try await encoder.prediction(from: encIn)
        guard let audioEmbeds = encOut.featureValue(for: "audio_embeds")?.multiArrayValue else {
            throw CocoaError(.fileReadCorruptFile)
        }
        let audioLen = audioEmbeds.shape[1].intValue

        // 2. Embed fixed prompt prefix & suffix tokens.
        let beforeEmbeds = try await embedTokens(tokenizer.prompt.idsBefore)
        let afterEmbeds = try await embedTokens(tokenizer.prompt.idsAfter)

        let beforeLen = tokenizer.prompt.idsBefore.count
        let afterLen = tokenizer.prompt.idsAfter.count
        let maxAudioLen = max(0, Self.maxSeqLen - beforeLen - afterLen - 16)
        let usedAudioLen = min(audioLen, maxAudioLen)
        let contextLen = beforeLen + usedAudioLen + afterLen
        guard contextLen > 0, contextLen < Self.maxSeqLen else { return "" }

        // Concatenate [beforeEmbeds, audioEmbeds[:, :usedAudioLen, :], afterEmbeds] -> Float16 [1, contextLen, 1024].
        let prefillEmbeds = try MLMultiArray(shape: [1, NSNumber(value: contextLen), NSNumber(value: Self.hiddenSize)], dataType: .float16)
        copyFloat16Rows(from: beforeEmbeds, count: beforeLen, to: prefillEmbeds, dstRow: 0)
        copyFloat16Rows(from: audioEmbeds, count: usedAudioLen, to: prefillEmbeds, dstRow: beforeLen)
        copyFloat16Rows(from: afterEmbeds, count: afterLen, to: prefillEmbeds, dstRow: beforeLen + usedAudioLen)

        // 3. Prefill Qwen3Decoder with a fresh MLState KV-cache.
        let state = try makeDecoderState()
        let eosSet = Set(tokenizer.prompt.eosIDs)
        var nextToken = try await decodeStep(embeds: prefillEmbeds, startPos: 0, seqLen: contextLen, state: state)

        var generated: [Int] = []
        generated.reserveCapacity(64)
        var pos = contextLen

        while generated.count < Self.maxNewTokens && pos < Self.maxSeqLen {
            if eosSet.contains(nextToken) { break }
            generated.append(nextToken)
            let tokEmbed = try await embedTokens([nextToken])
            nextToken = try await decodeStep(embeds: tokEmbed, startPos: pos, seqLen: 1, state: state)
            pos += 1
        }

        return tokenizer.decode(generated)
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

    private func decodeStep(embeds: MLMultiArray, startPos: Int, seqLen: Int, state: MLState) async throws -> Int {
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
        let out = try await decoder.prediction(from: input, using: state)
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
