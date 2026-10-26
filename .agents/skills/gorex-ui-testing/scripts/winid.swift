// winid.swift OWNER prints the numbers of the on-screen, layer-0 windows
// whose owner is exactly OWNER, for screencapture -l, the largest first,
// each with its size in points.
import CoreGraphics

let owner = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : "GoRex"
let list = CGWindowListCopyWindowInfo([.optionOnScreenOnly], kCGNullWindowID) as! [[String: Any]]
var found: [(id: Int, w: Double, h: Double)] = []
for w in list where (w["kCGWindowOwnerName"] as? String) == owner && (w["kCGWindowLayer"] as? Int) == 0 {
    let b = w["kCGWindowBounds"] as! [String: Any]
    found.append((w["kCGWindowNumber"] as! Int, b["Width"] as! Double, b["Height"] as! Double))
}
for f in found.sorted(by: { $0.w * $0.h > $1.w * $1.h }) {
    print(f.id, Int(f.w), Int(f.h))
}
