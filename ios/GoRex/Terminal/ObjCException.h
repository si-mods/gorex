#import <Foundation/Foundation.h>

NS_ASSUME_NONNULL_BEGIN

/// Runs `block` inside an Objective-C `@try/@catch` and converts any thrown
/// `NSException` into an `NSError` so Swift callers can recover instead of
/// aborting with `SIGABRT` (for example Core ML `-[MLModel newState]`).
BOOL GoRexCatchObjCException(void (NS_NOESCAPE ^block)(void), NSError * _Nullable * _Nullable error);

NS_ASSUME_NONNULL_END
