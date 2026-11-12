// swift-tools-version:5.9
// sherpa-onnx's C API (https://github.com/k2-fsa/sherpa-onnx, Apache-2.0),
// with onnxruntime linked in (MIT): its prebuilt iOS framework, pinned.
import PackageDescription

let package = Package(
    name: "SherpaOnnx",
    platforms: [.iOS(.v13)],
    products: [.library(name: "SherpaOnnxC", targets: ["SherpaOnnxC"])],
    targets: [
        .binaryTarget(
            name: "SherpaOnnxC",
            url: "https://github.com/k2-fsa/sherpa-onnx/releases/download/xcframework/sherpa-onnx-v2.0.1-ios-shared-onnxruntime-static.xcframework.zip",
            checksum: "fc4c665e694322ced5393aafafaae6bb3e0a7449d98d5db60bc220ac7ac4cbc9"
        ),
    ]
)
