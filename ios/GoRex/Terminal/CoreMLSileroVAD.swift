import CoreML
import Foundation

/// Configuration for streaming Silero VAD hysteresis (IOS-033).
nonisolated struct VadSegmentationConfig: Sendable {
    var minSilenceDuration: Double = 0.8
    var silenceThresholdForSplit: Float = 0.45
    var negativeThreshold: Float? = 0.45
    var negativeThresholdOffset: Float = 0.15
    var speechPadding: Double = 0.03

    func effectiveNegativeThreshold(baseThreshold: Float) -> Float {
        if let neg = negativeThreshold { return neg }
        return max(0.01, baseThreshold - negativeThresholdOffset)
    }
}

nonisolated struct VadStreamEvent: Equatable, Sendable {
    enum Kind: Equatable, Sendable { case speechStart, speechEnd }
    let kind: Kind
    let sampleIndex: Int
}

nonisolated struct VadStreamState: Sendable {
    static let contextSize = 64
    static let stateSize = 128

    var context: [Float]
    var hidden: [Float]
    var cell: [Float]
    var processedSamples: Int
    var triggered: Bool
    var tempEndSample: Int?

    static func initial() -> VadStreamState {
        VadStreamState(
            context: [Float](repeating: 0, count: contextSize),
            hidden: [Float](repeating: 0, count: stateSize),
            cell: [Float](repeating: 0, count: stateSize),
            processedSamples: 0,
            triggered: false,
            tempEndSample: nil
        )
    }
}

nonisolated struct VadStreamResult: Sendable {
    let state: VadStreamState
    let event: VadStreamEvent?
    let probability: Float
}

/// Native Apple Core ML runner for Silero VAD v5/v6 (16 kHz, 512-sample windows)
/// replacing FluidAudio's VadManager (IOS-033).
actor CoreMLSileroVAD: Sendable {
    /// 256 ms chunk at 16 kHz (8 consecutive 512-sample Silero windows).
    static let chunkSize = 4096
    static let sampleRate = 16_000
    private static let windowSize = 512
    private static let inputSize = VadStreamState.contextSize + windowSize // 576

    private nonisolated(unsafe) let model: MLModel

    init(modelURL: URL) throws {
        let cfg = MLModelConfiguration()
        cfg.computeUnits = .cpuOnly // Tiny 1.5 MB LSTM runs sub-millisecond on CPU with zero ANE overhead.
        self.model = try MLModel(contentsOf: modelURL, configuration: cfg)
    }

    func processStreamingChunk(
        _ audioChunk: [Float],
        state: VadStreamState,
        config: VadSegmentationConfig
    ) throws -> VadStreamResult {
        var ctx = state.context
        var h = state.hidden
        var c = state.cell

        // Pad or trim to chunkSize (4096) using repeat-last padding.
        var samples = audioChunk
        if samples.count < Self.chunkSize {
            let pad = samples.last ?? 0
            samples.append(contentsOf: repeatElement(pad, count: Self.chunkSize - samples.count))
        } else if samples.count > Self.chunkSize {
            samples = Array(samples.prefix(Self.chunkSize))
        }

        // Fast path for digital silence.
        let silent = samples.allSatisfy { abs($0) <= 1e-10 }
        var maxProb: Float = 0

        if !silent {
            let audioArr = try MLMultiArray(shape: [1, NSNumber(value: Self.inputSize)], dataType: .float32)
            let hArr = try MLMultiArray(shape: [1, NSNumber(value: VadStreamState.stateSize)], dataType: .float32)
            let cArr = try MLMultiArray(shape: [1, NSNumber(value: VadStreamState.stateSize)], dataType: .float32)
            let aPtr = audioArr.dataPointer.assumingMemoryBound(to: Float.self)
            let hPtr = hArr.dataPointer.assumingMemoryBound(to: Float.self)
            let cPtr = cArr.dataPointer.assumingMemoryBound(to: Float.self)

            let numWindows = Self.chunkSize / Self.windowSize
            for w in 0..<numWindows {
                let offset = w * Self.windowSize
                for i in 0..<VadStreamState.contextSize { aPtr[i] = ctx[i] }
                for i in 0..<Self.windowSize { aPtr[VadStreamState.contextSize + i] = samples[offset + i] }
                for i in 0..<VadStreamState.stateSize { hPtr[i] = h[i]; cPtr[i] = c[i] }

                let input = try MLDictionaryFeatureProvider(dictionary: [
                    "audio": MLFeatureValue(multiArray: audioArr),
                    "h_state": MLFeatureValue(multiArray: hArr),
                    "c_state": MLFeatureValue(multiArray: cArr),
                ])
                let out = try model.prediction(from: input)
                if let pArr = out.featureValue(for: "prob")?.multiArrayValue {
                    let p = pArr.dataPointer.assumingMemoryBound(to: Float.self)[0]
                    if p > maxProb { maxProb = p }
                }
                if let nh = out.featureValue(for: "new_h")?.multiArrayValue {
                    let p = nh.dataPointer.assumingMemoryBound(to: Float.self)
                    for i in 0..<VadStreamState.stateSize { h[i] = p[i] }
                }
                if let nc = out.featureValue(for: "new_c")?.multiArrayValue {
                    let p = nc.dataPointer.assumingMemoryBound(to: Float.self)
                    for i in 0..<VadStreamState.stateSize { c[i] = p[i] }
                }
                ctx = Array(samples[(offset + Self.windowSize - VadStreamState.contextSize)..<(offset + Self.windowSize)])
            }
        } else {
            ctx = Array(samples.suffix(VadStreamState.contextSize))
        }

        return Self.stepStateMachine(
            probability: maxProb,
            chunkSampleCount: audioChunk.count,
            context: ctx,
            hidden: h,
            cell: c,
            state: state,
            config: config
        )
    }

    /// Pure state machine (exposed for deterministic unit tests).
    nonisolated static func stepStateMachine(
        probability: Float,
        chunkSampleCount: Int,
        context: [Float],
        hidden: [Float],
        cell: [Float],
        state: VadStreamState,
        config: VadSegmentationConfig
    ) -> VadStreamResult {
        var next = state
        next.context = context
        next.hidden = hidden
        next.cell = cell
        next.processedSamples += chunkSampleCount

        let threshold: Float = config.negativeThreshold.map { min(1.0, $0 + config.negativeThresholdOffset) } ?? 0.5
        let negativeThreshold = config.effectiveNegativeThreshold(baseThreshold: threshold)
        let speechPadSamples = Int(config.speechPadding * Double(sampleRate))
        let minSilenceSamples = Int(config.minSilenceDuration * Double(sampleRate))

        var event: VadStreamEvent?
        if probability >= threshold {
            next.tempEndSample = nil
            if !next.triggered {
                next.triggered = true
                let startSample = max(0, next.processedSamples - speechPadSamples - chunkSampleCount)
                event = VadStreamEvent(kind: .speechStart, sampleIndex: startSample)
            }
        } else if probability < negativeThreshold && next.triggered {
            if next.tempEndSample == nil {
                next.tempEndSample = next.processedSamples
            }
            if let silenceStart = next.tempEndSample,
               next.processedSamples - silenceStart >= minSilenceSamples {
                let endSample = max(0, silenceStart + speechPadSamples - chunkSampleCount)
                next.triggered = false
                next.tempEndSample = nil
                event = VadStreamEvent(kind: .speechEnd, sampleIndex: endSample)
            }
        }
        return VadStreamResult(state: next, event: event, probability: probability)
    }
}
