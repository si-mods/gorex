import Accelerate
import Foundation

/// Kaldi-compatible 80-bin log-Mel filterbank frontend with Low Frame Rate
/// (LFR) stacking for Fun-ASR-Nano. Matches kaldi-native-fbank's settings in
/// sherpa-onnx: 16 kHz, 25 ms Hamming window, 10 ms hop, snip_edges=false,
/// dither=0, preemph=0.97, remove_dc_offset=true, samples scaled by 32768,
/// lfr_m=7, lfr_n=6 (output dimension 560).
nonisolated struct FbankFrontend: Sendable {
    static let sampleRate = 16_000
    static let frameLength = 400 // 25 ms at 16 kHz
    static let frameShift = 160  // 10 ms at 16 kHz
    static let fftSize = 512     // next power of 2 >= 400
    static let numBins = 80
    static let lfrM = 7
    static let lfrN = 6
    static let outputDim = numBins * lfrM // 560

    private let window: [Float]
    private let melFilters: [[(bin: Int, weight: Float)]]

    init() {
        // Kaldi Hamming window: 0.54 - 0.46 * cos(2*pi*n / (N - 1))
        var w = [Float](repeating: 0, count: Self.frameLength)
        let denom = Float(Self.frameLength - 1)
        for n in 0..<Self.frameLength {
            w[n] = 0.54 - 0.46 * cos(2 * .pi * Float(n) / denom)
        }
        self.window = w
        self.melFilters = Self.buildMelFilterbank()
    }

    /// Computes LFR-stacked log-Mel features as a flat row-major Float array
    /// of shape [numFrames, 560]. Returns ([], 0) for empty or silent audio.
    func extract(_ samples: [Float]) -> (features: [Float], numFrames: Int) {
        guard !samples.isEmpty else { return ([], 0) }
        let n = samples.count

        // Kaldi snip_edges=false frame count: (n + frameShift/2) / frameShift
        let rawFrames = (n + Self.frameShift / 2) / Self.frameShift
        guard rawFrames > 0 else { return ([], 0) }

        // Scale normalized [-1, 1] floats to int16 range (normalize_samples=false).
        var scaled = [Float](repeating: 0, count: n)
        var scale: Float = 32_768.0
        vDSP_vsmul(samples, 1, &scale, &scaled, 1, vDSP_Length(n))

        let log2n = vDSP_Length(9) // 2^9 = 512
        guard let fftSetup = vDSP_create_fftsetup(log2n, FFTRadix(kFFTRadix2)) else {
            return ([], 0)
        }
        defer { vDSP_destroy_fftsetup(fftSetup) }

        let halfFFT = Self.fftSize / 2
        var realBuf = [Float](repeating: 0, count: halfFFT)
        var imagBuf = [Float](repeating: 0, count: halfFFT)
        var frame = [Float](repeating: 0, count: Self.fftSize)
        var power = [Float](repeating: 0, count: halfFFT + 1)

        var fbank = [Float](repeating: 0, count: rawFrames * Self.numBins)
        let eps: Float = 1.1920929e-7 // FLT_EPSILON (Kaldi floor)

        for f in 0..<rawFrames {
            // Kaldi snip_edges=false: frame center at f * frameShift + frameShift/2
            let center = f * Self.frameShift + Self.frameShift / 2
            let start = center - Self.frameLength / 2

            // Reflect-pad at edges as kaldi-native-fbank does when snip_edges=false.
            for i in 0..<Self.frameLength {
                var idx = start + i
                if idx < 0 {
                    idx = -idx - 1
                }
                if idx >= n {
                    idx = 2 * n - 1 - idx
                }
                if idx < 0 { idx = 0 }
                if idx >= n { idx = n - 1 }
                frame[i] = scaled[idx]
            }
            for i in Self.frameLength..<Self.fftSize {
                frame[i] = 0
            }

            // Remove DC offset (subtract frame mean).
            var mean: Float = 0
            vDSP_meanv(frame, 1, &mean, vDSP_Length(Self.frameLength))
            var negMean = -mean
            vDSP_vsadd(frame, 1, &negMean, &frame, 1, vDSP_Length(Self.frameLength))

            // Pre-emphasis: x[i] -= 0.97 * x[i-1] (in reverse order; x[0] *= 0.03).
            for i in stride(from: Self.frameLength - 1, through: 1, by: -1) {
                frame[i] -= 0.97 * frame[i - 1]
            }
            frame[0] *= 0.03

            // Apply Hamming window.
            vDSP_vmul(frame, 1, window, 1, &frame, 1, vDSP_Length(Self.frameLength))

            // Real FFT (512-point) via Accelerate split-complex.
            realBuf.withUnsafeMutableBufferPointer { rPtr in
                imagBuf.withUnsafeMutableBufferPointer { iPtr in
                    var split = DSPSplitComplex(realp: rPtr.baseAddress!, imagp: iPtr.baseAddress!)
                    frame.withUnsafeBytes { raw in
                        let cPtr = raw.bindMemory(to: DSPComplex.self).baseAddress!
                        vDSP_ctoz(cPtr, 2, &split, 1, vDSP_Length(halfFFT))
                    }
                    vDSP_fft_zrip(fftSetup, &split, 1, log2n, FFTDirection(FFT_FORWARD))
                    // vDSP_fft_zrip scales by 2; divide power by 4.
                    // DC is in realp[0], Nyquist is in imagp[0].
                    let dc = split.realp[0] * 0.5
                    let nyq = split.imagp[0] * 0.5
                    power[0] = dc * dc
                    power[halfFFT] = nyq * nyq
                    for k in 1..<halfFFT {
                        let re = split.realp[k] * 0.5
                        let im = split.imagp[k] * 0.5
                        power[k] = re * re + im * im
                    }
                }
            }

            // Apply Mel filterbank and natural log.
            let rowBase = f * Self.numBins
            for m in 0..<Self.numBins {
                var energy: Float = 0
                for tap in melFilters[m] {
                    energy += tap.weight * power[tap.bin]
                }
                fbank[rowBase + m] = log(max(energy, eps))
            }
        }

        // Apply LFR stacking (lfr_m = 7, lfr_n = 6), matching sherpa-onnx ApplyLFR.
        let outFrames = (rawFrames - Self.lfrM) / Self.lfrN + 1
        guard outFrames > 0 else { return ([], 0) }
        var lfr = [Float](repeating: 0, count: outFrames * Self.outputDim)
        for i in 0..<outFrames {
            let srcOff = i * Self.lfrN * Self.numBins
            let dstOff = i * Self.outputDim
            lfr.replaceSubrange(dstOff..<(dstOff + Self.outputDim), with: fbank[srcOff..<(srcOff + Self.outputDim)])
        }

        // Digital silence check: all LFR features identical.
        if let first = lfr.first, lfr.allSatisfy({ $0 == first }) {
            return ([], 0)
        }
        return (lfr, outFrames)
    }

    private static func hzToMel(_ hz: Float) -> Float {
        1127.0 * log(1.0 + hz / 700.0)
    }

    private static func buildMelFilterbank() -> [[(bin: Int, weight: Float)]] {
        let lowFreq: Float = 20.0
        let highFreq = Float(sampleRate) / 2.0
        let melLow = hzToMel(lowFreq)
        let melHigh = hzToMel(highFreq)
        let melDelta = (melHigh - melLow) / Float(numBins + 1)
        let fftBinWidth = Float(sampleRate) / Float(fftSize)
        let halfFFT = fftSize / 2

        var filters = [[(bin: Int, weight: Float)]](repeating: [], count: numBins)
        for m in 0..<numBins {
            let leftMel = melLow + Float(m) * melDelta
            let centerMel = melLow + Float(m + 1) * melDelta
            let rightMel = melLow + Float(m + 2) * melDelta
            var taps: [(Int, Float)] = []
            for k in 0...halfFFT {
                let mel = hzToMel(Float(k) * fftBinWidth)
                if mel > leftMel && mel < rightMel {
                    let w: Float = mel <= centerMel
                        ? (mel - leftMel) / (centerMel - leftMel)
                        : (rightMel - mel) / (rightMel - centerMel)
                    if w > 0 {
                        taps.append((k, w))
                    }
                }
            }
            filters[m] = taps
        }
        return filters
    }
}
