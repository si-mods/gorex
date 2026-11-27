#!/usr/bin/env python3
"""Convert Silero VAD (snakers4/silero-vad) and SenseVoiceSmall (iic/SenseVoiceSmall)
to Apple Core ML (IOS-033), removing the FluidAudio / NemoTextProcessing dependency.

Produces:
  - SileroVAD.mlpackage        (16 kHz, 576-sample [64 ctx + 512 new] -> prob, h, c; FP32)
  - SenseVoiceSmall.mlpackage  (LFR [1, T, 560] + query_ids [1, 4] -> token_ids [1, 4+T]; INT8)
  - tokens.json                (copied from iic/SenseVoiceSmall)

Usage:
  /tmp/sv-venv/bin/python ios/scripts/convert-sensevoice-vad-coreml.py \
      --src /tmp/sv-vad-src \
      --out /tmp/sv-vad-out \
      --verify-audio /tmp/sv-vad-src/example/zh.mp3
"""

import argparse
import json
import math
import os
import re
import shutil
import subprocess
import time
import numpy as np
import torch
import torch.nn as nn
import torch.nn.functional as F
import coremltools as ct
import coremltools.optimize as cto


# ---------------------------------------------------------------------------
# 1. SenseVoiceSmall (SenseVoiceEncoderSmall + CMVN + CTC argmax)
# ---------------------------------------------------------------------------

def load_am_mvn(path: str):
    """Parse Kaldi am.mvn (<AddShift> and <Rescale>) -> (shift[560], scale[560])."""
    text = open(path).read()
    blocks = re.findall(r"<LearnRateCoef>\s+0\s+\[([^\]]+)\]", text)
    assert len(blocks) == 2, f"expected 2 CMVN vectors in {path}, got {len(blocks)}"
    shift = np.fromstring(blocks[0], sep=" ", dtype=np.float32)
    scale = np.fromstring(blocks[1], sep=" ", dtype=np.float32)
    assert shift.shape == (560,) and scale.shape == (560,)
    return shift, scale


class SinusoidalPE(nn.Module):
    def __init__(self, max_len: int = 1808, dim: int = 560):
        super().__init__()
        positions = torch.arange(1, max_len + 1, dtype=torch.float32)
        log_inc = math.log(10000.0) / (dim / 2 - 1)
        inv_ts = torch.exp(torch.arange(dim // 2, dtype=torch.float32) * (-log_inc))
        scaled = positions[:, None] * inv_ts[None, :]
        pe = torch.cat([torch.sin(scaled), torch.cos(scaled)], dim=-1)
        self.register_buffer("pe", pe.unsqueeze(0), persistent=False)

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        return x + self.pe[:, : x.shape[1], :].to(x.dtype)


class MultiHeadedAttentionSANM(nn.Module):
    def __init__(self, n_head: int, in_feat: int, n_feat: int, kernel_size: int = 11):
        super().__init__()
        self.h = n_head
        self.d_k = n_feat // n_head
        self.linear_out = nn.Linear(n_feat, n_feat)
        self.linear_q_k_v = nn.Linear(in_feat, n_feat * 3)
        self.fsmn_block = nn.Conv1d(
            n_feat, n_feat, kernel_size, stride=1, padding=(kernel_size - 1) // 2, groups=n_feat, bias=False
        )

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        b, t, _ = x.shape
        qkv = self.linear_q_k_v(x)
        q, k, v = torch.chunk(qkv, 3, dim=-1)
        fsmn_memory = self.fsmn_block(v.transpose(1, 2)).transpose(1, 2) + v
        q_h = q.view(b, t, self.h, self.d_k).transpose(1, 2)
        k_h = k.view(b, t, self.h, self.d_k).transpose(1, 2)
        v_h = v.view(b, t, self.h, self.d_k).transpose(1, 2)
        scores = torch.matmul(q_h * (self.d_k ** -0.5), k_h.transpose(-2, -1))
        attn = torch.softmax(scores, dim=-1)
        att_out = torch.matmul(attn, v_h).transpose(1, 2).contiguous().view(b, t, self.h * self.d_k)
        return self.linear_out(att_out) + fsmn_memory


class PositionwiseFFN(nn.Module):
    def __init__(self, idim: int, hidden_units: int):
        super().__init__()
        self.w_1 = nn.Linear(idim, hidden_units)
        self.w_2 = nn.Linear(hidden_units, idim)

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        return self.w_2(F.relu(self.w_1(x)))


class EncoderLayerSANM(nn.Module):
    def __init__(self, in_size: int, size: int, n_head: int = 4, ffn_dim: int = 2048, kernel_size: int = 11):
        super().__init__()
        self.in_size = in_size
        self.size = size
        self.self_attn = MultiHeadedAttentionSANM(n_head, in_size, size, kernel_size)
        self.feed_forward = PositionwiseFFN(size, ffn_dim)
        self.norm1 = nn.LayerNorm(in_size)
        self.norm2 = nn.LayerNorm(size)

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        residual = x
        x = self.self_attn(self.norm1(x))
        if self.in_size == self.size:
            x = residual + x
        return x + self.feed_forward(self.norm2(x))


class SenseVoiceSmallModel(nn.Module):
    def __init__(self, cmvn_shift: np.ndarray, cmvn_scale: np.ndarray):
        super().__init__()
        self.register_buffer("cmvn_shift", torch.from_numpy(cmvn_shift).view(1, 1, 560))
        self.register_buffer("cmvn_scale", torch.from_numpy(cmvn_scale).view(1, 1, 560))
        self.embed = nn.Embedding(16, 560)
        self.pe = SinusoidalPE(max_len=1808, dim=560)
        self.encoders0 = nn.ModuleList([EncoderLayerSANM(560, 512, 4, 2048, 11)])
        self.encoders = nn.ModuleList([EncoderLayerSANM(512, 512, 4, 2048, 11) for _ in range(49)])
        self.after_norm = nn.LayerNorm(512)
        self.tp_encoders = nn.ModuleList([EncoderLayerSANM(512, 512, 4, 2048, 11) for _ in range(20)])
        self.tp_norm = nn.LayerNorm(512)
        self.ctc_lo = nn.Linear(512, 25055)

    def forward(self, speech: torch.Tensor, query_ids: torch.Tensor) -> torch.Tensor:
        # Apply CMVN to raw LFR features, then prepend 4 query embeddings [lang, 1, 2, textnorm].
        x = (speech + self.cmvn_shift) * self.cmvn_scale
        q = self.embed(query_ids)  # [1, 4, 560]
        x = torch.cat([q, x], dim=1)
        x = self.pe(x * (512.0 ** 0.5))
        for layer in self.encoders0:
            x = layer(x)
        for layer in self.encoders:
            x = layer(x)
        x = self.after_norm(x)
        for layer in self.tp_encoders:
            x = layer(x)
        x = self.tp_norm(x)
        logits = self.ctc_lo(x)
        return torch.argmax(logits, dim=-1).to(torch.int32)


# ---------------------------------------------------------------------------
# 2. Silero VAD (16 kHz, 576-sample window: 64 context + 512 new -> prob, h, c)
# ---------------------------------------------------------------------------

class SileroVAD16k(nn.Module):
    def __init__(self, jit_path: str):
        super().__init__()
        jit = torch.jit.load(jit_path, map_location="cpu")
        m = jit._model
        sd = m.state_dict()
        # STFT: reflection pad right by 64, conv1d(1, 258, kernel=256, stride=128)
        self.stft_basis = nn.Parameter(sd["stft.forward_basis_buffer"].clone(), requires_grad=False)
        # 4 encoder blocks (reparam_conv + ReLU)
        self.enc0 = nn.Conv1d(129, 128, kernel_size=3, stride=1, padding=1)
        self.enc1 = nn.Conv1d(128, 64, kernel_size=3, stride=2, padding=1)
        self.enc2 = nn.Conv1d(64, 64, kernel_size=3, stride=2, padding=1)
        self.enc3 = nn.Conv1d(64, 128, kernel_size=3, stride=1, padding=1)
        for idx, conv in enumerate([self.enc0, self.enc1, self.enc2, self.enc3]):
            conv.weight.data.copy_(sd[f"encoder.{idx}.reparam_conv.weight"])
            conv.bias.data.copy_(sd[f"encoder.{idx}.reparam_conv.bias"])
        # Explicit LSTMCell weights (avoids PyTorch 2.15 aten::unsafe_chunk in coremltools)
        self.rnn_ih = nn.Linear(128, 512)
        self.rnn_hh = nn.Linear(128, 512)
        self.rnn_ih.weight.data.copy_(sd["decoder.rnn.weight_ih"])
        self.rnn_ih.bias.data.copy_(sd["decoder.rnn.bias_ih"])
        self.rnn_hh.weight.data.copy_(sd["decoder.rnn.weight_hh"])
        self.rnn_hh.bias.data.copy_(sd["decoder.rnn.bias_hh"])
        # Final 1x1 conv + sigmoid
        self.out_conv = nn.Conv1d(128, 1, kernel_size=1)
        self.out_conv.weight.data.copy_(sd["decoder.decoder.2.weight"])
        self.out_conv.bias.data.copy_(sd["decoder.decoder.2.bias"])

    def forward(self, audio: torch.Tensor, h: torch.Tensor, c: torch.Tensor):
        # audio: [1, 576] (64 context + 512 new samples)
        x = F.pad(audio, (0, 64), mode="reflect").unsqueeze(1)  # [1, 1, 640]
        ft = F.conv1d(x, self.stft_basis, stride=128)  # [1, 258, 4]
        real = ft[:, :129, :]
        imag = ft[:, 129:, :]
        mag = torch.sqrt(real * real + imag * imag + 1e-12)
        x = F.relu(self.enc0(mag))
        x = F.relu(self.enc1(x))
        x = F.relu(self.enc2(x))
        x = F.relu(self.enc3(x))  # [1, 128, 1]
        gates = self.rnn_ih(x.squeeze(-1)) + self.rnn_hh(h)
        i_g = torch.sigmoid(gates[:, :128])
        f_g = torch.sigmoid(gates[:, 128:256])
        g_g = torch.tanh(gates[:, 256:384])
        o_g = torch.sigmoid(gates[:, 384:])
        c_new = f_g * c + i_g * g_g
        h_new = o_g * torch.tanh(c_new)
        out = torch.sigmoid(self.out_conv(F.relu(h_new).unsqueeze(-1))).squeeze(-1)  # [1, 1]
        return out, h_new, c_new


# ---------------------------------------------------------------------------
# 3. Fbank + CTC decode helpers (for PyTorch vs Core ML parity verification)
# ---------------------------------------------------------------------------

def load_mp3_16k(path: str) -> np.ndarray:
    cmd = ["ffmpeg", "-v", "error", "-i", path, "-ar", "16000", "-ac", "1", "-f", "f32le", "-"]
    raw = subprocess.check_output(cmd)
    return np.frombuffer(raw, dtype=np.float32).copy()


def extract_fbank_lfr(samples: np.ndarray) -> np.ndarray:
    sr, flen, fshift, nfft, nmels = 16000, 400, 160, 512, 80
    n = len(samples)
    raw_frames = (n + fshift // 2) // fshift
    scaled = samples.astype(np.float32) * 32768.0
    win = (0.54 - 0.46 * np.cos(2.0 * np.pi * np.arange(flen) / (flen - 1))).astype(np.float32)

    def hz2mel(hz): return 1127.0 * np.log(1.0 + hz / 700.0)
    mel_pts = np.linspace(hz2mel(20.0), hz2mel(sr / 2.0), nmels + 2)
    freqs = np.arange(nfft // 2 + 1) * (sr / nfft)
    mels = hz2mel(freqs)
    fb = np.zeros((nmels, nfft // 2 + 1), dtype=np.float32)
    for m in range(nmels):
        l, c, r = mel_pts[m], mel_pts[m + 1], mel_pts[m + 2]
        fb[m] = np.maximum(0.0, np.minimum((mels - l) / (c - l), (r - mels) / (r - c)))

    fbank = np.zeros((raw_frames, nmels), dtype=np.float32)
    for f in range(raw_frames):
        center = f * fshift + fshift // 2
        start = center - flen // 2
        idx = np.arange(start, start + flen)
        idx = np.where(idx < 0, -idx - 1, idx)
        idx = np.where(idx >= n, 2 * n - 1 - idx, idx)
        idx = np.clip(idx, 0, n - 1)
        frame = scaled[idx].copy()
        frame -= frame.mean()
        pre = np.empty_like(frame)
        pre[0] = frame[0] - 0.97 * frame[0]
        pre[1:] = frame[1:] - 0.97 * frame[:-1]
        spec = np.fft.rfft(pre * win, n=nfft)
        power = (spec.real ** 2 + spec.imag ** 2).astype(np.float32)
        fbank[f] = np.log(np.maximum(fb @ power, 1.1920929e-7))

    lfr_m, lfr_n = 7, 6
    out_frames = (raw_frames - lfr_m) // lfr_n + 1
    lfr = np.zeros((out_frames, nmels * lfr_m), dtype=np.float32)
    for i in range(out_frames):
        lfr[i] = fbank[i * lfr_n : i * lfr_n + lfr_m].reshape(-1)
    return lfr


def ctc_decode(ids: np.ndarray, tokens: list[str]) -> str:
    prev = -1
    pieces = []
    for tid in ids.tolist():
        if tid != 0 and tid != prev:
            if 0 <= tid < len(tokens):
                pieces.append(tokens[tid])
        prev = tid
    raw = "".join(pieces).replace("\u2581", " ")
    return re.sub(r"<\|[^|]*\|>", "", raw).strip()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--src", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--verify-audio", nargs="*", default=[])
    args = ap.parse_args()
    os.makedirs(args.out, exist_ok=True)
    t0 = time.time()

    # 1. Convert SileroVAD.mlpackage
    print("Loading & verifying Silero VAD ...")
    vad_jit = torch.jit.load(os.path.join(args.src, "silero_vad.jit"), map_location="cpu")
    vad = SileroVAD16k(os.path.join(args.src, "silero_vad.jit")).eval()
    test_wav = torch.randn(1, 512)
    vad_jit.reset_states()
    ref_p = vad_jit(test_wav, 16000).item()
    with torch.no_grad():
        audio_576 = torch.cat([torch.zeros(1, 64), test_wav], dim=1)
        our_p, _, _ = vad(audio_576, torch.zeros(1, 128), torch.zeros(1, 128))
    print(f"Silero VAD parity diff: {abs(ref_p - our_p.item()):.2e} (ref={ref_p:.6f}, ours={our_p.item():.6f})")
    assert abs(ref_p - our_p.item()) < 1e-4

    traced_vad = torch.jit.trace(vad, (torch.zeros(1, 576), torch.zeros(1, 128), torch.zeros(1, 128)))
    ml_vad = ct.convert(
        traced_vad,
        inputs=[
            ct.TensorType(name="audio", shape=(1, 576), dtype=np.float32),
            ct.TensorType(name="h_state", shape=(1, 128), dtype=np.float32),
            ct.TensorType(name="c_state", shape=(1, 128), dtype=np.float32),
        ],
        outputs=[
            ct.TensorType(name="prob", dtype=np.float32),
            ct.TensorType(name="new_h", dtype=np.float32),
            ct.TensorType(name="new_c", dtype=np.float32),
        ],
        compute_precision=ct.precision.FLOAT32,
        minimum_deployment_target=ct.target.iOS18,
    )
    vad_path = os.path.join(args.out, "SileroVAD.mlpackage")
    ml_vad.save(vad_path)
    print(f"Saved {vad_path}")

    # 2. Load SenseVoiceSmall
    print("Loading SenseVoiceSmall ...")
    shift, scale = load_am_mvn(os.path.join(args.src, "am.mvn"))
    sv = SenseVoiceSmallModel(shift, scale)
    ckpt = torch.load(os.path.join(args.src, "model.pt"), map_location="cpu", weights_only=False)
    sd = ckpt["state_dict"] if "state_dict" in ckpt else ckpt
    sv_sd = {}
    for k, v in sd.items():
        if k.startswith("encoder."):
            sv_sd[k[len("encoder."):]] = v
        elif k.startswith("ctc."):
            sv_sd[k[len("ctc."):]] = v
        elif k == "embed.weight":
            sv_sd[k] = v
    missing, unexpected = sv.load_state_dict(sv_sd, strict=False)
    missing = [m for m in missing if not m.startswith(("cmvn_", "pe."))]
    print(f"SenseVoiceSmall loaded: missing={len(missing)}, unexpected={len(unexpected)}")
    assert len(missing) == 0, f"missing keys: {missing[:5]}"
    sv.eval()

    tokens = json.load(open(os.path.join(args.src, "tokens.json")))
    shutil.copyfile(os.path.join(args.src, "tokens.json"), os.path.join(args.out, "tokens.json"))

    # PyTorch verification
    q_ids = torch.tensor([[0, 1, 2, 14]], dtype=torch.int32)  # auto lang + withitn
    pt_results = {}
    for wav_path in args.verify_audio:
        samples = load_mp3_16k(wav_path)
        lfr = extract_fbank_lfr(samples)
        with torch.no_grad():
            ids = sv(torch.from_numpy(lfr[None, :, :]), q_ids)[0].numpy()
        txt = ctc_decode(ids, tokens)
        pt_results[wav_path] = (lfr, txt)
        print(f"PyTorch [{os.path.basename(wav_path)}]: {txt!r}")

    # 3. Export SenseVoiceSmall.mlpackage (INT8)
    print("Tracing & converting SenseVoiceSmall.mlpackage (INT8) ...")
    traced_sv = torch.jit.trace(sv, (torch.randn(1, 64, 560), q_ids))
    ml_sv = ct.convert(
        traced_sv,
        inputs=[
            ct.TensorType(name="speech", shape=(1, ct.RangeDim(1, 1800, default=64), 560), dtype=np.float32),
            ct.TensorType(name="query_ids", shape=(1, 4), dtype=np.int32),
        ],
        outputs=[ct.TensorType(name="token_ids", dtype=np.int32)],
        compute_precision=ct.precision.FLOAT16,
        minimum_deployment_target=ct.target.iOS18,
    )
    ml_sv = cto.coreml.linear_quantize_weights(
        ml_sv,
        cto.coreml.OptimizationConfig(
            global_config=cto.coreml.OpLinearQuantizerConfig(mode="linear_symmetric", dtype="int8")
        ),
    )
    sv_path = os.path.join(args.out, "SenseVoiceSmall.mlpackage")
    ml_sv.save(sv_path)
    print(f"Saved {sv_path} (total elapsed: {time.time() - t0:.1f}s)")

    # 4. Core ML end-to-end verification
    if args.verify_audio:
        sv_cm = ct.models.MLModel(sv_path)
        for wav_path, (lfr, pt_txt) in pt_results.items():
            t1 = time.time()
            out = sv_cm.predict({"speech": lfr[None, :, :].astype(np.float32), "query_ids": q_ids.numpy()})
            cm_txt = ctc_decode(out["token_ids"][0], tokens)
            print(f"Core ML [{os.path.basename(wav_path)}] ({time.time() - t1:.2f}s): {cm_txt!r} (match={cm_txt == pt_txt})")


if __name__ == "__main__":
    main()
