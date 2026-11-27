import Foundation

/// Minimal byte-level BPE decoder for Qwen3-0.6B (used by CoreMLFunASR).
/// Prompt token IDs (system/user prefix and assistant suffix) are fixed and
/// loaded from prompt_ids.json, so runtime only decodes generated token IDs
/// back to UTF-8 text.
nonisolated struct QwenTokenizer: Sendable {
    struct PromptConfig: Codable, Sendable {
        let idsBefore: [Int]
        let idsAfter: [Int]
        let eosIDs: [Int]
        let padID: Int
    }

    let prompt: PromptConfig
    private let idToBytes: [[UInt8]]
    private let specialIDs: Set<Int>

    init(vocabURL: URL, promptURL: URL) throws {
        let promptData = try Data(contentsOf: promptURL)
        self.prompt = try JSONDecoder().decode(PromptConfig.self, from: promptData)

        let vocabData = try Data(contentsOf: vocabURL)
        let vocab = try JSONDecoder().decode([String: Int].self, from: vocabData)
        let maxID = max(vocab.values.max() ?? 0, prompt.eosIDs.max() ?? 0)
        let unicodeToByte = Self.gpt2UnicodeToByte()

        var table = [[UInt8]](repeating: [], count: maxID + 1)
        var specials = Set(prompt.eosIDs)
        specials.insert(prompt.padID)

        for (token, id) in vocab where id >= 0 && id <= maxID {
            if token.hasPrefix("<|") && token.hasSuffix("|>") {
                specials.insert(id)
                continue
            }
            var bytes: [UInt8] = []
            bytes.reserveCapacity(token.unicodeScalars.count)
            for scalar in token.unicodeScalars {
                if let b = unicodeToByte[scalar.value] {
                    bytes.append(b)
                } else {
                    bytes.append(contentsOf: String(scalar).utf8)
                }
            }
            table[id] = bytes
        }
        self.idToBytes = table
        self.specialIDs = specials
    }

    func decode(_ tokenIDs: [Int]) -> String {
        var bytes: [UInt8] = []
        bytes.reserveCapacity(tokenIDs.count * 4)
        for id in tokenIDs {
            guard id >= 0, id < idToBytes.count, !specialIDs.contains(id) else { continue }
            bytes.append(contentsOf: idToBytes[id])
        }
        return String(decoding: bytes, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
    }

    /// Inverse of GPT-2 / Qwen bytes_to_unicode() table.
    private static func gpt2UnicodeToByte() -> [UInt32: UInt8] {
        var bs: [Int] = []
        bs.append(contentsOf: Int(UInt8(ascii: "!"))...Int(UInt8(ascii: "~")))
        bs.append(contentsOf: 0xA1...0xAC)
        bs.append(contentsOf: 0xAE...0xFF)
        var cs = bs
        var n = 0
        for b in 0..<256 where !bs.contains(b) {
            bs.append(b)
            cs.append(256 + n)
            n += 1
        }
        var map: [UInt32: UInt8] = [:]
        map.reserveCapacity(256)
        for (b, c) in zip(bs, cs) {
            map[UInt32(c)] = UInt8(b)
        }
        return map
    }
}
