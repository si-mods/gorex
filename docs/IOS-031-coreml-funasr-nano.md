---
name: IOS-031
description: "Runs Fun-ASR-Nano on iOS with Apple Core ML instead of sherpa-onnx, removing SherpaOnnxC (15 to 25 MB IPA), with the models resident on the CPU (Latency). Use when you change iOS dictation models, Core ML speech conversion, or SherpaOnnxC removal."
metadata:
  created: "2026-11-27"
  updated: "2026-11-28"
---

# IOS-031: Core ML Fun-ASR-Nano for iOS dictation

Status: built.

Converted `FunAudioLLM/Fun-ASR-Nano-2512` to precompiled Apple Core ML (`.mlmodelc`) and published `funasr-nano-coreml-v1.tar.bz2` (550 MB compressed, 698 MB unpacked) under release `models-funasr-nano-coreml-v1`, with `SherpaOnnxC` removed from `ios/project.yml`.

This doc decides how Rex Go runs `Fun-ASR-Nano` natively on Apple `CoreML.framework` on iOS so we can remove `SherpaOnnxC` completely, cut 15 to 25 MB from the IPA, and move inference from 2 CPU cores to the Apple Neural Engine (ANE), while keeping a clean path for Core ML TTS later.

## Problem

`ios/GoRex/Terminal/Dictation.swift` currently links two separate speech runtimes: `FluidAudio` (Core ML for `SenseVoice Small` and Silero VAD) and `SherpaOnnxC` (full `sherpa-onnx` + `onnxruntime` C++ static library for `Fun-ASR-Nano` and `Qwen3-ASR 0.6B`). `SherpaOnnxC` adds roughly 15 to 25 MB to the iOS IPA download size and runs `Fun-ASR-Nano` on 2 CPU threads, which is slower and uses more battery than the Neural Engine.

## Goal

- Remove `SherpaOnnxC` (`ios/SherpaOnnx/Package.swift` and the `SherpaOnnx` dependency in `ios/project.yml`) and save 15 to 25 MB on the iOS IPA.
- Keep `Fun-ASR-Nano` Chinese-English mixed coding dictation quality identical to the current `sherpa-onnx` int8 model.
- Run `Fun-ASR-Nano` encoder and decoder on Core ML (ANE + GPU via `.all` compute units) with lower latency and lower power draw than 2-core ONNX CPU inference.
- Keep the Core ML pipeline modular so future on-device TTS (such as Core ML Kokoro) can reuse the same model download, caching, and Core ML runner patterns with zero new native runtimes.

## Non-goals

- Shipping model weights inside the app bundle (models stay on-demand downloads into `Application Support`, excluded from backups, as today).
- Keeping `Qwen3-ASR 0.6B` on `SherpaOnnxC` after removal (either convert `Qwen3-ASR 0.6B` to Core ML in a follow-up phase or retire it if `Fun-ASR-Nano` + `SenseVoice Small` cover all users).
- Implementing on-device TTS UI or synthesis in this doc (tracked separately when TTS is scheduled).

## Design

Users see the same `Fun-ASR-Nano` choice in Settings > Dictation and the same Composer / key-bar mic workflow. On first tap, Rex Go downloads the Core ML `Fun-ASR-Nano` model bundle (`.mlmodelc` packages + tokenizer JSON) from a pinned Hugging Face repo instead of the `.onnx` files, then runs all inference through Apple's built-in `CoreML.framework`.

### Model architecture and Core ML conversion

`Fun-ASR-Nano` consists of three stages that map cleanly to Core ML packages (`iOS 18+` / `mlprogram` with `palettize` / `int8` / `int4` weight compression):

| Stage | Input | Output | Core ML target |
| --- | --- | --- | --- |
| 1. Audio Frontend (Swift / Accelerate) | 16 kHz mono `Float` PCM (up to 20 s) | 80-dim log-Mel Fbank + LFR stacking + CMVN | Pure Swift using `Accelerate` (`vDSP`), zero external C++ |
| 2. `EncoderAdaptor.mlpackage` | Stacked Fbank `[1, T, D]` | Audio embeddings `[1, T', H]` aligned to Qwen3 hidden size | FP16 / INT8 weight-compressed MLProgram on ANE |
| 3. `Qwen3Decoder.mlpackage` | Token/audio embeddings + `MLState` KV-cache | Next-token logits `[1, 1, V]` | Stateful `MLState` Core ML model (iOS 18+ / iOS 26 native) with INT4/INT8 LUT quantization on ANE |

Using iOS 18+ **stateful Core ML (`MLState`)** for the Qwen3-0.6B KV-cache avoids copying cache tensors across Swift and Core ML on every decode step, keeping autoregressive decoding fast and memory-bounded on iPhone.

### Swift runtime components

All code lives in `ios/GoRex/Terminal/` with zero third-party C++ dependencies:

- `FbankFrontend.swift`: Computes 80-bin log-Mel filterbank features (25 ms Hamming/Povey window, 10 ms hop, LFR frame stacking, CMVN) with `Accelerate` (`vDSP_DFT`). Unit-tested against reference vectors exported from `Fun-ASR-Nano` Python / `kaldi-native-fbank` within `1e-4` tolerance.
- `QwenTokenizer.swift`: Minimal byte-level BPE tokenizer reading `vocab.json`, `merges.txt`, and special tokens from the downloaded model directory (prompt template `<|im_start|>system...<|im_end|>` + audio placeholder + `<|im_start|>assistant\n`).
- `CoreMLFunASR.swift`: Implements the existing `Recogniser` protocol in `Dictation.swift`:
  1. Runs `FbankFrontend` on the 16 kHz PCM buffer.
  2. Runs `EncoderAdaptor` to produce audio embeddings.
  3. Prefills `Qwen3Decoder` with system/user prompt token embeddings spliced with audio embeddings into a fresh `MLState` KV-cache.
  4. Runs greedy decoding (`argmax` on logits) until `<|im_end|>` or `maxTokens`, then decodes token IDs with `QwenTokenizer` and applies inverse text normalization / punctuation cleanup.
- `Dictation.swift`: Switches `.funASRNano` from `Sherpa` to `CoreMLFunASR`, updates the pinned Hugging Face repo and file sizes, and removes `import SherpaOnnxC` and `actor Sherpa` once `Qwen3-ASR` is migrated or retired.

### Model conversion and hosting

- Add a reproducible conversion script `ios/scripts/convert-funasr-coreml.py` (using `torch` + `coremltools >= 8.0`) that exports `EncoderAdaptor.mlpackage`, `Embedding.mlpackage` (or FP16 binary table), and stateful `Qwen3Decoder.mlpackage` with INT4/INT8 weight compression, validates parity against PyTorch on test audio, and packages them for Hugging Face (`si-mods/funasr-nano-coreml`).
- Core ML compiles `.mlpackage` to `.mlmodelc` on-device once after download (`MLModel.compileModel(at:)`), or ships precompiled `.mlmodelc` tarballs for instant load.

### Future Core ML TTS compatibility

Because Core ML is part of iOS (`0 MB` binary overhead), adding on-device TTS later (such as Core ML Kokoro or Matcha, or FluidAudio's Core ML TTS) requires only adding a small Swift frontend + `.mlpackage` download without re-introducing ONNX Runtime or `SherpaOnnxC`.

## Latency

Dictation spun after the mic went off for minutes on a phone: `CoreMLFunASR` loaded `EncoderAdaptor` and `Qwen3Decoder` for each phrase on `.all` (staged after #1244 to keep both from being resident at once). Core ML compiles an `.all` model for the GPU at its first prediction, and a model loaded again compiles again. Measured natively on an Apple M2 Pro Mac with the release models, a 4 s phrase took 35 to 43 s (encoder's first prediction 19 to 29 s, decoder prefill 8 to 10 s), and a 14.5 s dictation with pauses, given in real time to `Listening`, finished 320 s after the mic went off.

Fun-ASR-Nano now runs on `.cpuOnly` on a device as on the simulator, with all three models loaded once in `CoreMLFunASR.init` and kept while the recogniser is; each phrase takes a fresh `MLState` only. Measured on the same Mac, with the models resident:

| Compute units | Load (encoder + decoder) | First phrase | Later phrases (encoder / prefill / token) | Footprint, peak |
| --- | --- | --- | --- | --- |
| `.cpuOnly` | 1.4 + 0.02 s (7.5 s once, cold) | as later | 0.1 s / 0.3 s / 0.12 to 0.18 s | 80 MB, 117 MB |
| `.cpuAndNeuralEngine` | 30 s | as later | 0.05 s / 0.2 s / 0.13 s | 200 MB, 296 MB |
| `.all` | 1.8 s | 32 s (GPU compile) | 0.05 s / 0.1 s / 0.04 s | 590 MB and growing, 601 MB |

The CPU decodes as fast as the Neural Engine, loads 20 times faster and holds under half its memory; its footprint stays far below the 570 MB of weights, which stay mapped from disk. `.all` is fastest per token once compiled, but its compile is what the `MpsGraphValidator` crash of #1244 ran out of memory in, and its footprint grows past what an iPhone allows. With the fix, the same 14.5 s dictation finishes 2.0 s after the mic (1.5 s for 17 s of Chinese), at a peak footprint of 180 MB against 512 MB before.

Two bounds go with it. A listening stops guessing once the mic is off: what is left in the ring is only settled, so finish waits for the last phrase, not for guesses at it. A phrase decodes at most one token per encoder frame (about 16 a second, past any speech rate), so a decode that loops on noise ends with the phrase, and a cancelled listening stops at the next token.

`DictationTests.testListeningEndsSoonAfterTheMic` feeds real speech in real time through `Listening` and requires finish within 10 s of the mic going off; on the simulator, whose Core ML is slower than the Mac's, Fun-ASR-Nano takes 6 to 7 s and SenseVoice 0.4 to 0.6 s. A phone's CPU is not measured here.

## Alternatives

- **Minimal `sherpa-onnx` + reduced-ops ONNX Runtime build**: Keeps upstream `.onnx` weights directly, but still ships ~8 to 12 MB of ONNX Runtime + C++ code in the IPA and stays on 2-core CPU inference instead of ANE.
- **Apple iOS 26 `SpeechAnalyzer`**: Zero binary size and zero model storage, but replaces `Fun-ASR-Nano` rather than preserving its Chinese-English technical dictation accuracy. Can still be offered as an extra engine alongside Core ML `Fun-ASR-Nano`.
- **Keep `SherpaOnnxC` as-is**: Zero engineering effort, but permanently carries 15 to 25 MB of unused ONNX/TTS/diarization code in every TestFlight and App Store download.

## Migration

Users who previously downloaded `sherpa-onnx` `.onnx` files under `Application Support/FluidAudio/funASRNano` will have the old `.onnx` files cleaned up automatically on upgrade, and the next mic tap prompts to download the smaller Core ML `Fun-ASR-Nano` package (INT4/INT8 quantized, ~350 to 500 MB vs ~1 GB ONNX).

## Tests

- **Frontend parity test**: `FbankFrontendTests` compares Swift `vDSP` output against a golden `.bin` tensor from Python `Fun-ASR-Nano` on a 3-second WAV fixture (`max abs diff < 1e-4`).
- **Tokenizer parity test**: `QwenTokenizerTests` verifies encode/decode round-trip on Chinese, English, code identifiers, and special tokens against `Qwen3-0.6B/tokenizer.json`.
- **End-to-end dictation parity**: Run `CoreMLFunASR` on a real iPhone against 10 recorded Chinese-English coding phrases, comparing WER/CER and wall-clock latency against current `Sherpa` `Fun-ASR-Nano`.
- **Package size verification**: Compare TestFlight `.ipa` size before and after removing `SherpaOnnxC` (expecting 15 to 25 MB drop).

## Phases

1. **Phase 1 — Conversion and parity benchmark**: Write `ios/scripts/convert-funasr-coreml.py`, export `Fun-ASR-Nano` to Core ML (`MLState` KV-cache + INT4/INT8 compression), verify output parity and latency on macOS/iPhone, and publish pinned weights to Hugging Face.
2. **Phase 2 — Swift Core ML runner**: Implement `FbankFrontend.swift`, `QwenTokenizer.swift`, and `CoreMLFunASR.swift` behind `Dictation.Model.funASRNano` with unit tests.
3. **Phase 3 — Remove `SherpaOnnxC`**: Decide fate of `Qwen3-ASR 0.6B` (convert or retire), delete `ios/SherpaOnnx/Package.swift` and `SherpaOnnxC` from `ios/project.yml`, clean up legacy `.onnx` cache files, and measure final IPA size.

## Open questions

- **What should happen to `Qwen3-ASR 0.6B` in Phase 3?** Options: (1) also convert `Qwen3-ASR 0.6B` to Core ML so both models stay available, or (2) retire `Qwen3-ASR 0.6B` and keep `SenseVoice Small` + Core ML `Fun-ASR-Nano`. Recommendation: option 2 first to ship the 15 to 25 MB IPA win immediately, then add Core ML `Qwen3-ASR` only if needed. Owner: user.
- **Weight quantization target for `Qwen3Decoder`**: INT4 palettization (~350 MB download, faster ANE bandwidth) vs INT8 (~600 MB download, closer to current ONNX int8). Phase 1 will benchmark both on real Chinese-English prompts before locking the default. Owner: user.
