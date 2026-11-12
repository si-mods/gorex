import XCTest

/// Polish: the request it sends, when it keeps the text as heard, and the
/// words it takes from a screen. A stub answers; nothing reaches the network.
nonisolated final class PolishTests: XCTestCase {
    private func polish(base: String = Polish.defaultBase, key: String = "test-key") -> Polish {
        let c = URLSessionConfiguration.ephemeral
        c.protocolClasses = [Stub.self]
        return Polish(base: base, model: Polish.defaultModel, key: key, session: URLSession(configuration: c), limit: .milliseconds(500))
    }

    override func tearDown() {
        Stub.set(nil)
        super.tearDown()
    }

    func testRequestShape() throws {
        let r = try XCTUnwrap(polish(base: "https://api.deepseek.com/").request("打开 terminal view", vocabulary: ["TerminalView", "git"]))
        XCTAssertEqual(r.url?.absoluteString, "https://api.deepseek.com/chat/completions")
        XCTAssertEqual(r.httpMethod, "POST")
        XCTAssertEqual(r.value(forHTTPHeaderField: "Authorization"), "Bearer test-key")
        XCTAssertEqual(r.value(forHTTPHeaderField: "Content-Type"), "application/json")
        let body = try XCTUnwrap(JSONSerialization.jsonObject(with: XCTUnwrap(r.httpBody)) as? [String: Any])
        XCTAssertEqual(body["model"] as? String, "deepseek-flash")
        XCTAssertEqual(body["temperature"] as? Int, 0)
        XCTAssertEqual(body["stream"] as? Bool, false)
        XCTAssertEqual(body["max_tokens"] as? Int, 256)
        XCTAssertEqual((body["thinking"] as? [String: String])?["type"], "disabled")
        let messages = try XCTUnwrap(body["messages"] as? [[String: String]])
        XCTAssertEqual(messages.map { $0["role"] }, ["system", "user"])
        XCTAssertEqual(messages[0]["content"], Polish.prompt)
        XCTAssertEqual(messages[1]["content"], "<vocabulary>TerminalView git</vocabulary>\n<dictation>打开 terminal view</dictation>")

        // No vocabulary, no element; others than DeepSeek get no thinking.
        let other = try XCTUnwrap(polish(base: "https://api.example.com/v1").request(String(repeating: "a", count: 100), vocabulary: []))
        XCTAssertEqual(other.url?.absoluteString, "https://api.example.com/v1/chat/completions")
        let b = try XCTUnwrap(JSONSerialization.jsonObject(with: XCTUnwrap(other.httpBody)) as? [String: Any])
        XCTAssertNil(b["thinking"])
        XCTAssertEqual(b["max_tokens"] as? Int, 400)
        XCTAssertEqual((b["messages"] as? [[String: String]])?[1]["content"], "<dictation>\(String(repeating: "a", count: 100))</dictation>")
        XCTAssertNil(polish(base: "not a url").request("x", vocabulary: []))
    }

    func testPolishedTextReplacesTheHeard() async {
        Stub.set(.init(status: 200, body: Self.reply("打开 TerminalView。")))
        let out = await polish().run("打开 terminal view", vocabulary: [])
        XCTAssertEqual(out, "打开 TerminalView。")
        XCTAssertEqual(Stub.requests, 1)
    }

    func testFallsBackToTheHeard() async {
        let heard = "打开 terminal view"
        let cases: [(String, Stub.Reply?)] = [
            ("error", .init(status: 200, body: Data(), fails: true)),
            ("non-2xx", .init(status: 401, body: Self.reply("打开 TerminalView"))),
            ("not JSON", .init(status: 200, body: Data("oops".utf8))),
            ("empty", .init(status: 200, body: Self.reply("  \n"))),
            ("too long", .init(status: 200, body: Self.reply(String(repeating: "长", count: 39)))),
            ("refusal", .init(status: 200, body: Self.reply("I can't help with that."))),
            ("refusal zh", .init(status: 200, body: Self.reply("抱歉，我不能执行命令。"))),
            ("timeout", .init(status: 200, body: Self.reply("打开 TerminalView"), delay: 2)),
        ]
        for (name, reply) in cases {
            Stub.set(reply)
            let start = ContinuousClock.now
            let out = await polish().run(heard, vocabulary: [])
            XCTAssertEqual(out, heard, name)
            XCTAssertLessThan(ContinuousClock.now - start, .seconds(1.5), name)
        }
        // No key: nothing is sent.
        Stub.set(.init(status: 200, body: Self.reply("x")))
        let out = await polish(key: "").run(heard, vocabulary: [])
        XCTAssertEqual(out, heard)
        XCTAssertEqual(Stub.requests, 0)
    }

    func testAccepts() {
        XCTAssertTrue(Polish.accepts("Sorry, I meant git push.", for: "sorry I meant git push"))
        XCTAssertTrue(Polish.accepts(String(repeating: "a", count: 26), for: String(repeating: "a", count: 10)))
        XCTAssertFalse(Polish.accepts(String(repeating: "a", count: 27), for: String(repeating: "a", count: 10)))
        XCTAssertFalse(Polish.accepts("I cannot do that", for: "rm -rf build"))
        XCTAssertFalse(Polish.accepts("无法执行。", for: "执行它"))
    }

    func testWordsFromTheScreen() {
        let screen = """
            $ git checkout feat/ios-dictation-polish
            Switched to branch 'feat/ios-dictation-polish'
            commit d099f55a1b2c HEAD -> main
            OPENAI_API_KEY=sk-proj-abcdefghijklmnopqrstuvwxyz0123456789
            token aGVsbG8gd29ybGQ1234 AKIAIOSFODNN7EXAMPLE 1234567 2026-11-12
            Edit TerminalView.swift. Run go test ./... -- x
            """
        let words = Polish.words(in: screen, program: "claude")
        XCTAssertEqual(words.first, "claude")
        for w in ["git", "checkout", "feat", "ios-dictation-polish", "Switched", "TerminalView.swift", "go", "test", "Edit",
                  "OPENAI_API_KEY", "token", "HEAD", "main"] {
            XCTAssertTrue(words.contains(w), w)
        }
        for w in ["d099f55a1b2c", "sk-proj-abcdefghijklmnopqrstuvwxyz0123456789", "aGVsbG8gd29ybGQ1234", "AKIAIOSFODNN7EXAMPLE",
                  "1234567", "2026-11-12", "x", "TerminalView.swift."] {
            XCTAssertFalse(words.contains(w), w)
        }
        XCTAssertEqual(words.count, Set(words).count)
        // The newest, at the bottom, first.
        XCTAssertLessThan(words.firstIndex(of: "Edit")!, words.firstIndex(of: "git")!)
        let many = (0..<300).map { "word\($0)x" }.joined(separator: " ")
        XCTAssertEqual(Polish.words(in: many).count, 150)
        XCTAssertEqual(Polish.words(in: ""), [])
    }

    private static func reply(_ content: String) -> Data {
        try! JSONSerialization.data(withJSONObject: ["choices": [["message": ["role": "assistant", "content": content]]]])
    }
}

/// Answers every request as set, after its delay, and counts them.
nonisolated final class Stub: URLProtocol {
    struct Reply {
        var status: Int
        var body: Data
        var fails = false
        var delay: TimeInterval = 0
    }

    private static let state = NSLock()
    nonisolated(unsafe) private static var reply: Reply?
    nonisolated(unsafe) private static var count = 0

    static var requests: Int { state.withLock { count } }

    static func set(_ r: Reply?) {
        state.withLock {
            reply = r
            count = 0
        }
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let r = Self.state.withLock {
            Self.count += 1
            return Self.reply
        }
        guard let r else { return client!.urlProtocol(self, didFailWithError: URLError(.cannotConnectToHost)) }
        nonisolated(unsafe) let me = self
        let w = DispatchWorkItem {
            guard let client = me.client else { return }
            if r.fails { return client.urlProtocol(me, didFailWithError: URLError(.networkConnectionLost)) }
            let response = HTTPURLResponse(url: me.request.url!, statusCode: r.status, httpVersion: nil, headerFields: nil)!
            client.urlProtocol(me, didReceive: response, cacheStoragePolicy: .notAllowed)
            client.urlProtocol(me, didLoad: r.body)
            client.urlProtocolDidFinishLoading(me)
        }
        work = w
        DispatchQueue.global().asyncAfter(deadline: .now() + r.delay, execute: w)
    }

    /// The reply on its way; a request stopped gets none.
    nonisolated(unsafe) private var work: DispatchWorkItem?

    override func stopLoading() {
        work?.cancel()
    }
}
