//go:build darwin && cgo && macintegration && !bindings

#import <Cocoa/Cocoa.h>
#import <dispatch/dispatch.h>

#include "menu_bar_darwin.h"

static void mobileEgressContractRunOnMainAsync(dispatch_block_t block) {
    if ([NSThread isMainThread]) {
        block();
        return;
    }
    dispatch_async(dispatch_get_main_queue(), block);
}

static void mobileEgressContractRunOnMainSync(dispatch_block_t block) {
    if ([NSThread isMainThread]) {
        block();
        return;
    }
    dispatch_sync(dispatch_get_main_queue(), block);
}

@interface MobileEgressSentinelDelegate : NSObject <NSApplicationDelegate>
@end

@implementation MobileEgressSentinelDelegate
@end

static MobileEgressSentinelDelegate *mobileEgressSentinelDelegate = nil;
static id mobileEgressPreviousDelegate = nil;

void mobile_egress_menu_bar_contract_run_application_loop(void) {
    @autoreleasepool {
        [NSApplication sharedApplication];
        [NSApp run];
    }
}

void mobile_egress_menu_bar_contract_stop_application_loop(void) {
    mobileEgressContractRunOnMainAsync(^{
        [NSApp stop:nil];
        NSEvent *wakeEvent = [NSEvent otherEventWithType:NSApplicationDefined
                                                location:NSZeroPoint
                                           modifierFlags:0
                                               timestamp:0
                                            windowNumber:0
                                                 context:nil
                                                 subtype:0
                                                   data1:0
                                                   data2:0];
        [NSApp postEvent:wakeEvent atStart:NO];
    });
}

void mobile_egress_menu_bar_contract_install_sentinel_delegate(void) {
    mobileEgressContractRunOnMainSync(^{
        if (mobileEgressSentinelDelegate == nil) {
            mobileEgressPreviousDelegate = [[NSApp delegate] retain];
            mobileEgressSentinelDelegate = [[MobileEgressSentinelDelegate alloc] init];
        }
        [NSApp setDelegate:mobileEgressSentinelDelegate];
    });
}

void mobile_egress_menu_bar_contract_remove_sentinel_delegate(void) {
    mobileEgressContractRunOnMainSync(^{
        if ([NSApp delegate] == mobileEgressSentinelDelegate) {
            [NSApp setDelegate:mobileEgressPreviousDelegate];
        }
        [mobileEgressSentinelDelegate release];
        mobileEgressSentinelDelegate = nil;
        [mobileEgressPreviousDelegate release];
        mobileEgressPreviousDelegate = nil;
    });
}

int mobile_egress_menu_bar_contract_delegate_preserved(void) {
    __block BOOL result = NO;
    mobileEgressContractRunOnMainSync(^{
        result = [NSApp delegate] == mobileEgressSentinelDelegate;
    });
    return result ? 1 : 0;
}
