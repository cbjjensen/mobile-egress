//go:build darwin && cgo && !bindings

#import <Cocoa/Cocoa.h>
#import <dispatch/dispatch.h>

#include "fatal_darwin.h"

static NSString *mobileEgressFatalString(const char *value) {
    if (value == NULL) {
        return @"";
    }
    NSString *result = [NSString stringWithUTF8String:value];
    return result == nil ? @"" : result;
}

void mobile_egress_show_fatal_alert(const char *heading, const char *body) {
    NSString *headingValue = [mobileEgressFatalString(heading) copy];
    NSString *bodyValue = [mobileEgressFatalString(body) copy];
    dispatch_block_t present = ^{
        @autoreleasepool {
            NSAlert *alert = [[NSAlert alloc] init];
            alert.window.title = @"ZFNF Mobile Egress";
            alert.messageText = headingValue;
            alert.informativeText = bodyValue;
            [alert addButtonWithTitle:@"OK"];
            [alert runModal];
            [alert release];
        }
        [headingValue release];
        [bodyValue release];
    };
    if ([NSThread isMainThread]) {
        present();
    } else {
        dispatch_sync(dispatch_get_main_queue(), present);
    }
}
