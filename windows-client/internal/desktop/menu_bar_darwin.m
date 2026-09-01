#import <Cocoa/Cocoa.h>
#import <dispatch/dispatch.h>

#include <stdint.h>
#include <stdatomic.h>
#include <stdlib.h>
#include <string.h>

#include "menu_bar_darwin.h"

// Wails owns NSApp's event loop and delegate. This bridge only owns its
// NSStatusItem and marshals every AppKit interaction onto the main thread.
extern void mobileEgressMenuBarShow(uintptr_t callback_handle);
extern void mobileEgressMenuBarQuit(uintptr_t callback_handle);
extern void mobileEgressMenuBarStopped(uintptr_t callback_handle);

static NSStatusItem *mobileEgressStatusItem = nil;
static NSMenuItem *mobileEgressStatusMenuItem = nil;
static uintptr_t mobileEgressActiveHandle = 0;
static _Atomic(uintptr_t) mobileEgressPendingHandle = 0;

#ifdef MOBILE_EGRESS_MENU_BAR_CONTRACT
static BOOL mobileEgressAllOperationsOnMainThread = YES;
#endif

static void mobileEgressRecordMainThread(void) {
#ifdef MOBILE_EGRESS_MENU_BAR_CONTRACT
    if (![NSThread isMainThread]) {
        mobileEgressAllOperationsOnMainThread = NO;
    }
#endif
}

static void mobileEgressRunOnMainAsync(dispatch_block_t block) {
    if ([NSThread isMainThread]) {
        block();
        return;
    }
    dispatch_async(dispatch_get_main_queue(), block);
}

static void mobileEgressRunOnMainSync(dispatch_block_t block) {
    if ([NSThread isMainThread]) {
        block();
        return;
    }
    dispatch_sync(dispatch_get_main_queue(), block);
}

@interface MobileEgressMenuTarget : NSObject {
    uintptr_t _callbackHandle;
}
- (instancetype)initWithCallbackHandle:(uintptr_t)callbackHandle;
- (void)showController:(id)sender;
- (void)quitController:(id)sender;
@end

static MobileEgressMenuTarget *mobileEgressMenuTarget = nil;

@implementation MobileEgressMenuTarget

- (instancetype)initWithCallbackHandle:(uintptr_t)callbackHandle {
    self = [super init];
    if (self != nil) {
        _callbackHandle = callbackHandle;
    }
    return self;
}

- (void)showController:(id)sender {
    (void)sender;
    mobileEgressRecordMainThread();
    [self retain];
    mobileEgressMenuBarShow(_callbackHandle);
    [self release];
}

- (void)quitController:(id)sender {
    (void)sender;
    mobileEgressRecordMainThread();
    [self retain];
    mobileEgressMenuBarQuit(_callbackHandle);
    [self release];
}

@end

static void mobileEgressRemoveMenuBarOnMain(void) {
    mobileEgressRecordMainThread();
    if (mobileEgressStatusItem != nil) {
        [[NSStatusBar systemStatusBar] removeStatusItem:mobileEgressStatusItem];
        mobileEgressStatusItem = nil;
    }
    mobileEgressStatusMenuItem = nil;
    if (mobileEgressMenuTarget != nil) {
        [mobileEgressMenuTarget release];
        mobileEgressMenuTarget = nil;
    }
    mobileEgressActiveHandle = 0;
}

static NSString *mobileEgressOwnedString(const char *value) {
    if (value == NULL) {
        return [[NSString alloc] initWithString:@""];
    }
    NSString *result = [[NSString alloc] initWithUTF8String:value];
    if (result == nil) {
        return [[NSString alloc] initWithString:@""];
    }
    return result;
}

void mobile_egress_menu_bar_start(
    const void *icon_bytes,
    size_t icon_length,
    const char *tooltip,
    const char *initial_status,
    const char *show_title,
    const char *show_tooltip,
    const char *quit_title,
    const char *quit_tooltip,
    uintptr_t callback_handle) {
    NSData *iconData = nil;
    if (icon_bytes != NULL && icon_length > 0) {
        iconData = [[NSData alloc] initWithBytes:icon_bytes length:icon_length];
    }
    NSString *tooltipValue = mobileEgressOwnedString(tooltip);
    NSString *initialStatusValue = mobileEgressOwnedString(initial_status);
    NSString *showTitleValue = mobileEgressOwnedString(show_title);
    NSString *showTooltipValue = mobileEgressOwnedString(show_tooltip);
    NSString *quitTitleValue = mobileEgressOwnedString(quit_title);
    NSString *quitTooltipValue = mobileEgressOwnedString(quit_tooltip);
    atomic_store_explicit(&mobileEgressPendingHandle, callback_handle, memory_order_release);

    mobileEgressRunOnMainAsync(^{
        @autoreleasepool {
            mobileEgressRecordMainThread();
            uintptr_t expectedHandle = callback_handle;
            if (atomic_compare_exchange_strong_explicit(
                    &mobileEgressPendingHandle,
                    &expectedHandle,
                    0,
                    memory_order_acq_rel,
                    memory_order_acquire)) {
                if (mobileEgressStatusItem != nil) {
                    uintptr_t replacedHandle = mobileEgressActiveHandle;
                    mobileEgressRemoveMenuBarOnMain();
                    if (replacedHandle != 0) {
                        mobileEgressMenuBarStopped(replacedHandle);
                    }
                }

                mobileEgressActiveHandle = callback_handle;
                mobileEgressMenuTarget = [[MobileEgressMenuTarget alloc] initWithCallbackHandle:callback_handle];
                mobileEgressStatusItem = [[NSStatusBar systemStatusBar] statusItemWithLength:NSVariableStatusItemLength];
                NSStatusBarButton *button = [mobileEgressStatusItem button];
                [button setToolTip:tooltipValue];
                if (iconData != nil) {
                    NSImage *image = [[NSImage alloc] initWithData:iconData];
                    [image setTemplate:YES];
                    [button setImage:image];
                    [image release];
                }

                NSMenu *menu = [[NSMenu alloc] initWithTitle:@""];
                [menu setAutoenablesItems:NO];

                NSMenuItem *statusItem = [[NSMenuItem alloc] initWithTitle:initialStatusValue action:nil keyEquivalent:@""];
                [statusItem setEnabled:NO];
                [menu addItem:statusItem];
                mobileEgressStatusMenuItem = statusItem;
                [statusItem release];

                NSMenuItem *showItem = [[NSMenuItem alloc] initWithTitle:showTitleValue action:@selector(showController:) keyEquivalent:@""];
                [showItem setTarget:mobileEgressMenuTarget];
                [showItem setToolTip:showTooltipValue];
                [menu addItem:showItem];
                [showItem release];

                [menu addItem:[NSMenuItem separatorItem]];

                NSMenuItem *quitItem = [[NSMenuItem alloc] initWithTitle:quitTitleValue action:@selector(quitController:) keyEquivalent:@""];
                [quitItem setTarget:mobileEgressMenuTarget];
                [quitItem setToolTip:quitTooltipValue];
                [menu addItem:quitItem];
                [quitItem release];

                [mobileEgressStatusItem setMenu:menu];
                [menu release];
            }
        }

        [iconData release];
        [tooltipValue release];
        [initialStatusValue release];
        [showTitleValue release];
        [showTooltipValue release];
        [quitTitleValue release];
        [quitTooltipValue release];
    });
}

void mobile_egress_menu_bar_set_status(uintptr_t callback_handle, const char *status) {
    NSString *statusValue = mobileEgressOwnedString(status);
    mobileEgressRunOnMainAsync(^{
        mobileEgressRecordMainThread();
        if (mobileEgressActiveHandle == callback_handle && mobileEgressStatusMenuItem != nil) {
            [mobileEgressStatusMenuItem setTitle:statusValue];
        }
        [statusValue release];
    });
}

void mobile_egress_menu_bar_stop(uintptr_t callback_handle) {
    mobileEgressRunOnMainSync(^{
        mobileEgressRecordMainThread();
        if (mobileEgressActiveHandle == callback_handle) {
            mobileEgressRemoveMenuBarOnMain();
            mobileEgressMenuBarStopped(callback_handle);
            return;
        }
        uintptr_t expectedHandle = callback_handle;
        if (atomic_compare_exchange_strong_explicit(
                &mobileEgressPendingHandle,
                &expectedHandle,
                0,
                memory_order_acq_rel,
                memory_order_acquire)) {
            mobileEgressMenuBarStopped(callback_handle);
        }
    });
}

#ifdef MOBILE_EGRESS_MENU_BAR_CONTRACT

void mobile_egress_menu_bar_contract_reset_main_thread_checks(void) {
    mobileEgressRunOnMainSync(^{
        mobileEgressAllOperationsOnMainThread = YES;
    });
}

int mobile_egress_menu_bar_contract_all_operations_on_main_thread(void) {
    __block BOOL result = NO;
    mobileEgressRunOnMainSync(^{
        result = mobileEgressAllOperationsOnMainThread;
    });
    return result ? 1 : 0;
}

int mobile_egress_menu_bar_contract_menu_present(void) {
    __block BOOL result = NO;
    mobileEgressRunOnMainSync(^{
        result = mobileEgressStatusItem != nil;
    });
    return result ? 1 : 0;
}

char *mobile_egress_menu_bar_contract_copy_status(void) {
    __block NSString *result = nil;
    mobileEgressRunOnMainSync(^{
        result = [[mobileEgressStatusMenuItem title] copy];
    });
    if (result == nil) {
        return strdup("");
    }
    char *copy = strdup([result UTF8String]);
    [result release];
    return copy;
}

int mobile_egress_menu_bar_contract_click_show(void) {
    __block BOOL result = NO;
    mobileEgressRunOnMainSync(^{
        if (mobileEgressMenuTarget != nil) {
            [mobileEgressMenuTarget showController:nil];
            result = YES;
        }
    });
    return result ? 1 : 0;
}

int mobile_egress_menu_bar_contract_click_quit(void) {
    __block BOOL result = NO;
    mobileEgressRunOnMainSync(^{
        if (mobileEgressMenuTarget != nil) {
            [mobileEgressMenuTarget quitController:nil];
            result = YES;
        }
    });
    return result ? 1 : 0;
}

#endif
