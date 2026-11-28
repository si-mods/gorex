---
name: IOS-033
description: "Removes FluidAudio (and its 28 MB arm64 Rust NemoTextProcessing library) from iOS by running Silero VAD and SenseVoiceSmall on native Core ML with pure-Swift runners and GitHub Releases model delivery. Use when changing iOS dictation VAD, SenseVoiceSmall, or FluidAudio."
metadata:
  created: "2026-11-27"
  updated: "2026-11-28"
---

# IOS-033: Native Core ML Silero VAD and SenseVoiceSmall (remove FluidAudio)

Status: built.

This doc decides how Rex Go removes the `FluidAudio` Swift package (and its bundled `NemoTextProcessing.xcframework`, 28 MB arm64 / 197 MB universal Rust static library) by running `Silero VAD` and `SenseVoiceSmall` directly on Apple `CoreML.framework` with pure-Swift runners and GitHub Releases downloads.

## Problem

After `IOS-031` removed `SherpaOnnxC`, `FluidAudio` remained the only third-party speech dependency in `ios/project.yml`, solely for two Core ML models: `Silero VAD` (~2 MB) and `SenseVoiceSmall` (~225 MB). Keeping `FluidAudio` carries two costs:

1. **Binary & CI overhead**: `FluidAudio` links `NemoTextProcessing.xcframework` (Rust `text-processing-rs` + `rustfst` + NVIDIA NeMo ITN grammars, 28 MB for `ios-arm64` / 197 MB universal), even though `SenseVoiceSmall` never uses NeMo ITN (it produces inverse text normalization natively via its `withitn` query token).
2. **Hugging Face dependency**: `FluidAudio`'s `ModelHub` downloads Silero VAD and `SenseVoiceSmall` from `huggingface.co` / `hf-mirror.com`, which is frequently unreachable from mainland China — blocking even users who chose GitHub-hosted `Fun-ASR-Nano`, because Silero VAD is required by all models.

## Decision

1. **Native Core ML conversion (`ios/scripts/convert-sensevoice-vad-coreml.py`)**:
   - Convert upstream `snakers4/silero-vad` (`silero_vad.jit`, 16 kHz, 512-sample windows, stateful LSTM `h`/`c`) to `SileroVAD.mlmodelc` (FP32, ~1.5 MB).
   - Convert upstream `iic/SenseVoiceSmall` (`model.pt` + `am.mvn` CMVN baked into the input layer + `tokens.json`) to `SenseVoiceSmall.mlmodelc` (INT8 weight quantization, ~225 MB) taking `[1, T, 560]` LFR features directly from our existing pure-Swift `FbankFrontend` (`vDSP`, zero separate preprocessor model).
   - Package both as precompiled `.mlmodelc` tarballs on `si-mods/gorex` GitHub Releases (`models-sensevoice-vad-coreml-v1`), served via our existing 4-connection resumable `TarBz2` downloader.
2. **Pure-Swift runners**:
   - `CoreMLSileroVAD.swift`: `actor CoreMLSileroVAD` + `VadStreamState` + `VadSegmentationConfig` implementing Silero VAD streaming chunks (512 samples × 8 = 4096-sample chunks, 256 ms) and hysteresis (`speechStart` / `speechEnd`) matching the previous `VadManager` behavior.
   - `CoreMLSenseVoice.swift`: `actor CoreMLSenseVoice: Recogniser` reusing `FbankFrontend`, running `SenseVoiceSmall.mlmodelc`, performing vDSP-accelerated per-frame CTC argmax + collapse, SentencePiece detokenization, and `<|...|>` tag parsing (`nospeech` returns `""`).
3. **Clean removal**:
   - Remove `FluidAudio` from `ios/project.yml` and `ios/THIRD_PARTY_NOTICES.md`.
   - Reclaim legacy `FluidAudio` Hugging Face cache directories on disk so upgrading users recover storage.
   - `ios/project.yml` now has only one external Swift package (`GhosttyKit`).

## Compute units

`SenseVoiceSmall` runs on `.cpuOnly` on a device as on the simulator. On `.all`, Core ML compiles it for the GPU at the first phrase after each load (the model unloads a minute after the mic goes off), and measured natively on an Apple M2 Pro Mac that phrase took 17.5 s: a 14.5 s dictation showed no text until 18 s in, and finished 3.2 s after the mic at a 338 MB peak footprint. On the CPU a 4 s phrase takes 0.05 to 0.08 s and 17 s of speech 0.11 s, the first text shows 1.4 s in, and the same dictation finishes 0.14 s after the mic, at under 60 MB. See `IOS-031` (Latency) for Fun-ASR-Nano.

## Verification

- PyTorch vs Core ML end-to-end parity verification on `zh.mp3` and `en.mp3` for `SenseVoiceSmall` and speech-segment detection verification for `SileroVAD`.
- `xcodebuild test -only-testing:GoRexTests/DictationTests` on iOS Simulator with zero `FluidAudio` imports or linkage.
