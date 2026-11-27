#!/usr/bin/env python3
"""Convert FunAudioLLM/Fun-ASR-Nano-2512 to Apple Core ML (IOS-031).

Produces three Core ML packages plus tokenizer/prompt metadata:
  - EncoderAdaptor.mlpackage  (SenseVoiceEncoderSmall + 2-layer Transformer adaptor, INT8)
  - TokenEmbedding.mlpackage  (Qwen3-0.6B embed_tokens, INT8)
  - Qwen3Decoder.mlpackage    (28-layer Qwen3-0.6B with iOS 18+ MLState KV-cache, INT4)
  - vocab.json                (copied from Qwen3-0.6B)
  - prompt_ids.json           (pre-tokenized system/user prefix, assistant suffix, eos/pad IDs)

Usage:
  /tmp/funasr-coreml-venv/bin/python ios/scripts/convert-funasr-coreml.py \
      --src /tmp/funasr-nano-src \
      --out /tmp/funasr-coreml-out \
      --verify-audio /tmp/funasr-nano-src/example/zh.mp3
"""

import argparse
import json
import math
import os
import shutil
import subprocess
import time
import numpy as np
import torch
import torch.nn as nn
import torch.nn.functional as F
import coremltools as ct
import coremltools.optimize as cto
from transformers import AutoTokenizer


# ---------------------------------------------------------------------------
# 1. SenseVoiceEncoderSmall + Transformer AudioAdaptor (pure PyTorch, batch=1)
# ---------------------------------------------------------------------------

class SinusoidalPE(nn.Module):
    def __init__(self, max_len: int = 512, dim: int = 560):
        super().__init__()
        positions = torch.arange(1, max_len + 1, dtype=torch.float32)
        log_inc = math.log(10000.0) / (dim / 2 - 1)
        inv_ts = torch.exp(torch.arange(dim // 2, dtype=torch.float32) * (-log_inc))
        scaled = positions[:, None] * inv_ts[None, :]
        pe = torch.cat([torch.sin(scaled), torch.cos(scaled)], dim=-1)  # [max_len, dim]
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
        # FSMN depthwise conv memory on v
        fsmn_memory = self.fsmn_block(v.transpose(1, 2)).transpose(1, 2) + v
        # Multi-head self-attention (batch=1, unpadded -> no mask needed)
        q_h = q.view(b, t, self.h, self.d_k).transpose(1, 2)
        k_h = k.view(b, t, self.h, self.d_k).transpose(1, 2)
        v_h = v.view(b, t, self.h, self.d_k).transpose(1, 2)
        scores = torch.matmul(q_h * (self.d_k ** -0.5), k_h.transpose(-2, -1))
        attn = torch.softmax(scores, dim=-1)
        att_out = torch.matmul(attn, v_h).transpose(1, 2).contiguous().view(b, t, self.h * self.d_k)
        att_out = self.linear_out(att_out)
        return att_out + fsmn_memory


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
        residual = x
        x = residual + self.feed_forward(self.norm2(x))
        return x


class AdaptorAttention(nn.Module):
    def __init__(self, n_head: int = 8, n_feat: int = 1024):
        super().__init__()
        self.h = n_head
        self.d_k = n_feat // n_head
        self.linear_q = nn.Linear(n_feat, n_feat)
        self.linear_k = nn.Linear(n_feat, n_feat)
        self.linear_v = nn.Linear(n_feat, n_feat)
        self.linear_out = nn.Linear(n_feat, n_feat)

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        b, t, _ = x.shape
        q = self.linear_q(x).view(b, t, self.h, self.d_k).transpose(1, 2)
        k = self.linear_k(x).view(b, t, self.h, self.d_k).transpose(1, 2)
        v = self.linear_v(x).view(b, t, self.h, self.d_k).transpose(1, 2)
        scores = torch.matmul(q * (self.d_k ** -0.5), k.transpose(-2, -1))
        attn = torch.softmax(scores, dim=-1)
        out = torch.matmul(attn, v).transpose(1, 2).contiguous().view(b, t, self.h * self.d_k)
        return self.linear_out(out)


class AdaptorBlock(nn.Module):
    def __init__(self, dim: int = 1024, n_head: int = 8, ffn_dim: int = 256):
        super().__init__()
        self.self_attn = AdaptorAttention(n_head, dim)
        self.feed_forward = PositionwiseFFN(dim, ffn_dim)
        self.norm1 = nn.LayerNorm(dim)
        self.norm2 = nn.LayerNorm(dim)

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        x = x + self.self_attn(self.norm1(x))
        x = x + self.feed_forward(self.norm2(x))
        return x


class SenseVoiceEncoderAdaptor(nn.Module):
    def __init__(self):
        super().__init__()
        self.embed = SinusoidalPE(max_len=512, dim=560)
        self.encoders0 = nn.ModuleList([EncoderLayerSANM(560, 512, 4, 2048, 11)])
        self.encoders = nn.ModuleList([EncoderLayerSANM(512, 512, 4, 2048, 11) for _ in range(49)])
        self.after_norm = nn.LayerNorm(512)
        self.tp_encoders = nn.ModuleList([EncoderLayerSANM(512, 512, 4, 2048, 11) for _ in range(20)])
        self.tp_norm = nn.LayerNorm(512)
        # Audio adaptor
        self.linear1 = nn.Linear(512, 2048)
        self.linear2 = nn.Linear(2048, 1024)
        self.blocks = nn.ModuleList([AdaptorBlock(1024, 8, 256) for _ in range(2)])

    def forward(self, speech: torch.Tensor) -> torch.Tensor:
        x = speech * (512.0 ** 0.5)
        x = self.embed(x)
        for layer in self.encoders0:
            x = layer(x)
        for layer in self.encoders:
            x = layer(x)
        x = self.after_norm(x)
        for layer in self.tp_encoders:
            x = layer(x)
        x = self.tp_norm(x)
        # Adaptor
        x = self.linear2(F.relu(self.linear1(x)))
        for blk in self.blocks:
            x = blk(x)
        return x


# ---------------------------------------------------------------------------
# 2. Qwen3-0.6B Token Embedding + Stateful KV-Cache Decoder (iOS 18+ MLState)
# ---------------------------------------------------------------------------

class RMSNorm(nn.Module):
    """FP16-safe RMSNorm: normalizes by max(|x|) before squaring so x^2 never overflows FP16."""

    def __init__(self, dim: int, eps: float = 1e-6):
        super().__init__()
        self.weight = nn.Parameter(torch.ones(dim))
        self.eps = eps

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        scale = x.abs().amax(dim=-1, keepdim=True).clamp(min=1e-4)
        xs = x / scale
        inv = torch.rsqrt(xs.pow(2).mean(-1, keepdim=True) + self.eps / (scale * scale).clamp(min=1e-4))
        return (xs * inv) * self.weight


class TokenEmbeddingModel(nn.Module):
    def __init__(self, vocab_size: int = 151936, hidden_size: int = 1024):
        super().__init__()
        self.embed_tokens = nn.Embedding(vocab_size, hidden_size)

    def forward(self, token_ids: torch.Tensor) -> torch.Tensor:
        return self.embed_tokens(token_ids)


class Qwen3Attention(nn.Module):
    def __init__(self, hidden_size=1024, num_heads=16, num_kv_heads=8, head_dim=128, rms_eps=1e-6):
        super().__init__()
        self.num_heads = num_heads
        self.num_kv_heads = num_kv_heads
        self.head_dim = head_dim
        self.groups = num_heads // num_kv_heads
        self.q_proj = nn.Linear(hidden_size, num_heads * head_dim, bias=False)
        self.k_proj = nn.Linear(hidden_size, num_kv_heads * head_dim, bias=False)
        self.v_proj = nn.Linear(hidden_size, num_kv_heads * head_dim, bias=False)
        self.o_proj = nn.Linear(num_heads * head_dim, hidden_size, bias=False)
        self.q_norm = RMSNorm(head_dim, rms_eps)
        self.k_norm = RMSNorm(head_dim, rms_eps)

    def forward(self, x, cos, sin, causal_mask, key_cache, value_cache, layer_idx, start_pos):
        b, s, _ = x.shape
        q = self.q_norm(self.q_proj(x).view(b, s, self.num_heads, self.head_dim)).transpose(1, 2)
        k = self.k_norm(self.k_proj(x).view(b, s, self.num_kv_heads, self.head_dim)).transpose(1, 2)
        v = self.v_proj(x).view(b, s, self.num_kv_heads, self.head_dim).transpose(1, 2)

        # Apply RoPE
        half = self.head_dim // 2
        q = q * cos + torch.cat([-q[..., half:], q[..., :half]], dim=-1) * sin
        k = k * cos + torch.cat([-k[..., half:], k[..., :half]], dim=-1) * sin

        # Update stateful KV cache slice [layer_idx, :, :, start_pos:start_pos+s, :]
        end_pos = start_pos + s
        key_cache[layer_idx, :, :, start_pos:end_pos, :] = k
        value_cache[layer_idx, :, :, start_pos:end_pos, :] = v

        k_full = key_cache[layer_idx]  # [1, 8, max_seq, 128]
        v_full = value_cache[layer_idx]

        # GQA repeat_interleave (2x)
        k_rep = k_full.repeat_interleave(self.groups, dim=1)
        v_rep = v_full.repeat_interleave(self.groups, dim=1)

        scores = torch.matmul(q * (self.head_dim ** -0.5), k_rep.transpose(-2, -1)) + causal_mask
        attn = torch.softmax(scores, dim=-1)
        out = torch.matmul(attn, v_rep).transpose(1, 2).contiguous().view(b, s, self.num_heads * self.head_dim)
        return self.o_proj(out)


class Qwen3MLP(nn.Module):
    def __init__(self, hidden_size=1024, intermediate_size=3072):
        super().__init__()
        self.gate_proj = nn.Linear(hidden_size, intermediate_size, bias=False)
        self.up_proj = nn.Linear(hidden_size, intermediate_size, bias=False)
        self.down_proj = nn.Linear(intermediate_size, hidden_size, bias=False)

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        return self.down_proj(F.silu(self.gate_proj(x)) * self.up_proj(x))


class Qwen3Layer(nn.Module):
    def __init__(self):
        super().__init__()
        self.input_layernorm = RMSNorm(1024, 1e-6)
        self.self_attn = Qwen3Attention()
        self.post_attention_layernorm = RMSNorm(1024, 1e-6)
        self.mlp = Qwen3MLP()

    def forward(self, x, cos, sin, causal_mask, key_cache, value_cache, layer_idx, start_pos):
        x = x + self.self_attn(self.input_layernorm(x), cos, sin, causal_mask, key_cache, value_cache, layer_idx, start_pos)
        x = x + self.mlp(self.post_attention_layernorm(x))
        return x


class StatefulQwen3Decoder(nn.Module):
    def __init__(self, num_layers=28, max_seq=512, num_kv_heads=8, head_dim=128, vocab_size=151936, rope_theta=1_000_000.0):
        super().__init__()
        self.layers = nn.ModuleList([Qwen3Layer() for _ in range(num_layers)])
        self.norm = RMSNorm(1024, 1e-6)
        self.lm_head = nn.Linear(1024, vocab_size, bias=False)

        # Precompute RoPE cos/sin table [max_seq, head_dim]
        inv_freq = 1.0 / (rope_theta ** (torch.arange(0, head_dim, 2, dtype=torch.float32) / head_dim))
        t = torch.arange(max_seq, dtype=torch.float32)
        freqs = torch.outer(t, inv_freq)
        emb = torch.cat([freqs, freqs], dim=-1)
        self.register_buffer("cos_table", emb.cos(), persistent=False)
        self.register_buffer("sin_table", emb.sin(), persistent=False)

        # Stateful KV caches (exported as Core ML MLState)
        self.register_buffer("key_cache", torch.zeros(num_layers, 1, num_kv_heads, max_seq, head_dim))
        self.register_buffer("value_cache", torch.zeros(num_layers, 1, num_kv_heads, max_seq, head_dim))

    RESIDUAL_ALPHA = 0.1  # Scales residual stream 10x down so Layer 2 outlier (~1.25e5) fits FP16 (<65504)

    def forward(self, inputs_embeds: torch.Tensor, position_ids: torch.Tensor, causal_mask: torch.Tensor) -> torch.Tensor:
        start_pos = position_ids[0, 0]
        cos = self.cos_table[position_ids].unsqueeze(1).to(inputs_embeds.dtype)  # [1, 1, S, 128]
        sin = self.sin_table[position_ids].unsqueeze(1).to(inputs_embeds.dtype)
        x = inputs_embeds * self.RESIDUAL_ALPHA
        for i, layer in enumerate(self.layers):
            x = layer(x, cos, sin, causal_mask, self.key_cache, self.value_cache, i, start_pos)
        # Only compute logits for the last token: [1, 1, vocab_size]
        last = self.norm(x[:, -1:, :])
        return self.lm_head(last)


# ---------------------------------------------------------------------------
# 3. Fbank helper (for PyTorch & Core ML parity verification)
# ---------------------------------------------------------------------------

def extract_fbank_lfr(samples: np.ndarray) -> np.ndarray:
    sr, flen, fshift, nfft, nmels = 16000, 400, 160, 512, 80
    n = len(samples)
    raw_frames = (n + fshift // 2) // fshift
    scaled = samples.astype(np.float32) * 32768.0
    win = (0.54 - 0.46 * np.cos(2.0 * np.pi * np.arange(flen) / (flen - 1))).astype(np.float32)

    def hz2mel(hz): return 1127.0 * np.log(1.0 + hz / 700.0)
    mel_low, mel_high = hz2mel(20.0), hz2mel(sr / 2.0)
    mel_pts = np.linspace(mel_low, mel_high, nmels + 2)
    freqs = np.arange(nfft // 2 + 1) * (sr / nfft)
    mels = hz2mel(freqs)
    fb = np.zeros((nmels, nfft // 2 + 1), dtype=np.float32)
    for m in range(nmels):
        l, c, r = mel_pts[m], mel_pts[m + 1], mel_pts[m + 2]
        fb[m] = np.maximum(0.0, np.minimum((mels - l) / (c - l), (r - mels) / (r - c)))

    feats = np.zeros((raw_frames, nmels), dtype=np.float32)
    for f in range(raw_frames):
        center = f * fshift + fshift // 2
        start = center - flen // 2
        idxs = np.arange(start, start + flen)
        idxs = np.where(idxs < 0, -idxs - 1, idxs)
        idxs = np.where(idxs >= n, 2 * n - 1 - idxs, idxs)
        idxs = np.clip(idxs, 0, n - 1)
        frame = scaled[idxs].copy()
        frame -= frame.mean()
        frame[1:] -= 0.97 * frame[:-1]
        frame[0] *= 0.03
        frame *= win
        spec = np.fft.rfft(frame, n=nfft)
        power = (spec.real ** 2 + spec.imag ** 2).astype(np.float32)
        feats[f] = np.log(np.maximum(fb @ power, np.finfo(np.float32).eps))

    lfr_m, lfr_n = 7, 6
    out_frames = (raw_frames - lfr_m) // lfr_n + 1
    out = np.zeros((out_frames, nmels * lfr_m), dtype=np.float32)
    for i in range(out_frames):
        out[i] = feats[i * lfr_n : i * lfr_n + lfr_m].reshape(-1)
    return out


def load_mp3_16k(path: str) -> np.ndarray:
    raw = subprocess.check_output(
        ["ffmpeg", "-v", "error", "-i", path, "-ar", "16000", "-ac", "1", "-f", "f32le", "-"]
    )
    return np.frombuffer(raw, dtype=np.float32)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--src", required=True, help="Directory with model.pt and Qwen3-0.6B/")
    ap.add_argument("--out", required=True, help="Output directory for Core ML packages")
    ap.add_argument("--verify-audio", default="", help="Optional audio file to verify PyTorch vs Core ML")
    ap.add_argument("--skip-export", action="store_true", help="Only run PyTorch parity check")
    args = ap.parse_args()

    os.makedirs(args.out, exist_ok=True)
    t0 = time.time()
    print(f"Loading checkpoint from {args.src}/model.pt ...")
    sd = torch.load(os.path.join(args.src, "model.pt"), map_location="cpu", weights_only=True)
    if "state_dict" in sd:
        sd = sd["state_dict"]

    # 1. Load EncoderAdaptor weights
    enc = SenseVoiceEncoderAdaptor()
    enc_sd = {}
    for k, v in sd.items():
        if k.startswith("audio_encoder."):
            enc_sd[k[len("audio_encoder.") :]] = v.float()
        elif k.startswith("audio_adaptor."):
            enc_sd[k[len("audio_adaptor.") :]] = v.float()
    missing, unexpected = enc.load_state_dict(enc_sd, strict=False)
    print(f"EncoderAdaptor loaded: missing={len(missing)}, unexpected={len(unexpected)}")
    assert len(missing) == 0, f"missing encoder keys: {missing[:5]}"
    enc.eval()

    # 2. Load TokenEmbedding weights
    emb = TokenEmbeddingModel()
    emb.embed_tokens.weight.data.copy_(sd["llm.model.embed_tokens.weight"].float())
    emb.eval()

    # 3. Load StatefulQwen3Decoder weights
    dec = StatefulQwen3Decoder()
    dec_sd = {}
    for k, v in sd.items():
        if k.startswith("llm.model.layers."):
            dec_sd[k[len("llm.model.") :]] = v.float()
        elif k == "llm.model.norm.weight":
            dec_sd["norm.weight"] = v.float()
        elif k == "llm.lm_head.weight":
            dec_sd["lm_head.weight"] = v.float()
    missing, unexpected = dec.load_state_dict(dec_sd, strict=False)
    missing = [m for m in missing if m not in ("key_cache", "value_cache")]
    print(f"Qwen3Decoder loaded: missing={len(missing)}, unexpected={len(unexpected)}")
    assert len(missing) == 0, f"missing decoder keys: {missing[:5]}"
    # Scale residual output projections by RESIDUAL_ALPHA so all 28 layers stay within FP16 range.
    for layer in dec.layers:
        layer.self_attn.o_proj.weight.data *= StatefulQwen3Decoder.RESIDUAL_ALPHA
        layer.mlp.down_proj.weight.data *= StatefulQwen3Decoder.RESIDUAL_ALPHA
    dec.eval()
    del sd

    # 4. Write prompt_ids.json and copy vocab.json
    tok = AutoTokenizer.from_pretrained(os.path.join(args.src, "Qwen3-0.6B"))
    before_text = "<|im_start|>system\nYou are a helpful assistant.<|im_end|>\n<|im_start|>user\n语音转写："
    after_text = "<|im_end|>\n<|im_start|>assistant\n"
    ids_before = tok.encode(before_text, add_special_tokens=False)
    ids_after = tok.encode(after_text, add_special_tokens=False)
    prompt_cfg = {
        "idsBefore": ids_before,
        "idsAfter": ids_after,
        "eosIDs": [151645, 151643],
        "padID": 151643,
    }
    with open(os.path.join(args.out, "prompt_ids.json"), "w") as f:
        json.dump(prompt_cfg, f, indent=2)
    shutil.copyfile(os.path.join(args.src, "Qwen3-0.6B", "vocab.json"), os.path.join(args.out, "vocab.json"))
    print(f"Wrote prompt_ids.json (before={len(ids_before)}, after={len(ids_after)}) and vocab.json")

    # 5. Optional PyTorch verification on real audio
    pt_text = None
    if args.verify_audio:
        samples = load_mp3_16k(args.verify_audio)
        lfr = extract_fbank_lfr(samples)
        print(f"Audio {args.verify_audio}: {len(samples)/16000:.2f}s -> LFR {lfr.shape}")
        with torch.no_grad():
            audio_embeds = enc(torch.from_numpy(lfr).unsqueeze(0))
            eb = emb(torch.tensor([ids_before], dtype=torch.long))
            ea = emb(torch.tensor([ids_after], dtype=torch.long))
            prefill = torch.cat([eb, audio_embeds, ea], dim=1)
            ctx_len = prefill.shape[1]
            dec.key_cache.zero_()
            dec.value_cache.zero_()
            mask = torch.full((1, 1, ctx_len, 512), -65504.0)
            for i in range(ctx_len):
                mask[0, 0, i, : i + 1] = 0.0
            pos_ids = torch.arange(ctx_len, dtype=torch.long).unsqueeze(0)
            logits = dec(prefill, pos_ids, mask)
            nxt = int(logits[0, 0].argmax().item())
            gen = []
            pos = ctx_len
            while len(gen) < 128 and pos < 512 and nxt not in (151645, 151643):
                gen.append(nxt)
                te = emb(torch.tensor([[nxt]], dtype=torch.long))
                dmask = torch.full((1, 1, 1, 512), -65504.0)
                dmask[0, 0, 0, : pos + 1] = 0.0
                dpos = torch.tensor([[pos]], dtype=torch.long)
                logits = dec(te, dpos, dmask)
                nxt = int(logits[0, 0].argmax().item())
                pos += 1
            pt_text = tok.decode(gen, skip_special_tokens=True).strip()
            print(f"PyTorch transcription: {pt_text!r}")

    if args.skip_export:
        return

    # 6. Export EncoderAdaptor.mlpackage (INT8 weight compression)
    print("Tracing & converting EncoderAdaptor.mlpackage ...")
    ex_speech = torch.randn(1, 50, 560)
    traced_enc = torch.jit.trace(enc, ex_speech)
    ml_enc = ct.convert(
        traced_enc,
        inputs=[ct.TensorType(name="speech", shape=(1, ct.RangeDim(1, 350, default=50), 560), dtype=np.float32)],
        outputs=[ct.TensorType(name="audio_embeds", dtype=np.float16)],
        compute_precision=ct.precision.FLOAT16,
        minimum_deployment_target=ct.target.iOS18,
    )
    ml_enc = cto.coreml.linear_quantize_weights(
        ml_enc, cto.coreml.OptimizationConfig(global_config=cto.coreml.OpLinearQuantizerConfig(mode="linear_symmetric", dtype="int8"))
    )
    enc_path = os.path.join(args.out, "EncoderAdaptor.mlpackage")
    ml_enc.save(enc_path)
    print(f"Saved {enc_path}")

    # 7. Export TokenEmbedding.mlpackage (INT8 weight compression)
    print("Tracing & converting TokenEmbedding.mlpackage ...")
    ex_ids = torch.tensor([[1, 2, 3, 4]], dtype=torch.int32)
    traced_emb = torch.jit.trace(emb, ex_ids)
    ml_emb = ct.convert(
        traced_emb,
        inputs=[ct.TensorType(name="token_ids", shape=(1, ct.RangeDim(1, 64, default=1)), dtype=np.int32)],
        outputs=[ct.TensorType(name="embeds", dtype=np.float16)],
        compute_precision=ct.precision.FLOAT16,
        minimum_deployment_target=ct.target.iOS18,
    )
    ml_emb = cto.coreml.linear_quantize_weights(
        ml_emb, cto.coreml.OptimizationConfig(global_config=cto.coreml.OpLinearQuantizerConfig(mode="linear_symmetric", dtype="int8"))
    )
    emb_path = os.path.join(args.out, "TokenEmbedding.mlpackage")
    ml_emb.save(emb_path)
    print(f"Saved {emb_path}")

    # 8. Export StatefulQwen3Decoder.mlpackage (iOS 18+ MLState KV-cache + INT4 linear quantization)
    print("Tracing & converting Qwen3Decoder.mlpackage (stateful MLState + INT4) ...")
    dec.key_cache.zero_()
    dec.value_cache.zero_()
    ex_embeds = torch.randn(1, 8, 1024)
    ex_pos = torch.arange(8, dtype=torch.int32).unsqueeze(0)
    ex_mask = torch.zeros(1, 1, 8, 512)
    traced_dec = torch.jit.trace(dec, (ex_embeds, ex_pos, ex_mask))
    ml_dec = ct.convert(
        traced_dec,
        inputs=[
            ct.TensorType(name="inputs_embeds", shape=(1, ct.RangeDim(1, 512, default=1), 1024), dtype=np.float16),
            ct.TensorType(name="position_ids", shape=(1, ct.RangeDim(1, 512, default=1)), dtype=np.int32),
            ct.TensorType(name="causal_mask", shape=(1, 1, ct.RangeDim(1, 512, default=1), 512), dtype=np.float16),
        ],
        outputs=[ct.TensorType(name="logits", dtype=np.float16)],
        states=[
            ct.StateType(wrapped_type=ct.TensorType(shape=(28, 1, 8, 512, 128), dtype=np.float16), name="key_cache"),
            ct.StateType(wrapped_type=ct.TensorType(shape=(28, 1, 8, 512, 128), dtype=np.float16), name="value_cache"),
        ],
        compute_precision=ct.precision.FLOAT16,
        minimum_deployment_target=ct.target.iOS18,
    )
    ml_dec = cto.coreml.linear_quantize_weights(
        ml_dec,
        cto.coreml.OptimizationConfig(
            global_config=cto.coreml.OpLinearQuantizerConfig(mode="linear_symmetric", dtype="int4", granularity="per_block", block_size=32)
        ),
    )
    dec_path = os.path.join(args.out, "Qwen3Decoder.mlpackage")
    ml_dec.save(dec_path)
    print(f"Saved {dec_path} (total elapsed: {time.time() - t0:.1f}s)")

    # 9. Verify Core ML end-to-end on macOS!
    if args.verify_audio:
        print("Running Core ML end-to-end verification ...")
        t1 = time.time()
        enc_cm = ct.models.MLModel(enc_path)
        emb_cm = ct.models.MLModel(emb_path)
        dec_cm = ct.models.MLModel(dec_path)
        samples = load_mp3_16k(args.verify_audio)
        lfr = extract_fbank_lfr(samples)
        audio_e = enc_cm.predict({"speech": lfr[None, :, :].astype(np.float32)})["audio_embeds"]
        eb = emb_cm.predict({"token_ids": np.array([ids_before], dtype=np.int32)})["embeds"]
        ea = emb_cm.predict({"token_ids": np.array([ids_after], dtype=np.int32)})["embeds"]
        prefill = np.concatenate([eb, audio_e, ea], axis=1).astype(np.float16)
        ctx_len = prefill.shape[1]
        state = dec_cm.make_state()
        mask = np.full((1, 1, ctx_len, 512), -1e4, dtype=np.float16)
        for i in range(ctx_len):
            mask[0, 0, i, : i + 1] = 0.0
        pos_ids = np.arange(ctx_len, dtype=np.int32)[None, :]
        logits = dec_cm.predict({"inputs_embeds": prefill, "position_ids": pos_ids, "causal_mask": mask}, state=state)["logits"]
        nxt = int(np.argmax(logits[0, 0]))
        gen = []
        pos = ctx_len
        while len(gen) < 128 and pos < 512 and nxt not in (151645, 151643):
            gen.append(nxt)
            te = emb_cm.predict({"token_ids": np.array([[nxt]], dtype=np.int32)})["embeds"].astype(np.float16)
            dmask = np.full((1, 1, 1, 512), -1e4, dtype=np.float16)
            dmask[0, 0, 0, : pos + 1] = 0.0
            dpos = np.array([[pos]], dtype=np.int32)
            logits = dec_cm.predict({"inputs_embeds": te, "position_ids": dpos, "causal_mask": dmask}, state=state)["logits"]
            nxt = int(np.argmax(logits[0, 0]))
            pos += 1
        cm_text = tok.decode(gen, skip_special_tokens=True).strip()
        print(f"Core ML transcription ({time.time() - t1:.2f}s): {cm_text!r}")
        print(f"Parity match: {cm_text == pt_text}")


if __name__ == "__main__":
    main()
