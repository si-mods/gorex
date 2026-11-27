#import "ObjCException.h"

BOOL GoRexCatchObjCException(void (NS_NOESCAPE ^block)(void), NSError * _Nullable * _Nullable error) {
    @try {
        block();
        return YES;
    } @catch (NSException *exception) {
        if (error) {
            NSString *reason = exception.reason.length > 0 ? exception.reason : exception.name;
            *error = [NSError errorWithDomain:@"dev.gorex.objc"
                                         code:1
                                     userInfo:@{
                                         NSLocalizedDescriptionKey: reason ?: @"Objective-C exception",
                                         @"NSExceptionName": exception.name ?: @"",
                                     }];
        }
        return NO;
    }
}
