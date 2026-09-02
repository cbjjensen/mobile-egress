//go:build darwin && cgo && !bindings

#import <Foundation/Foundation.h>
#import <ServiceManagement/ServiceManagement.h>
#import <dispatch/dispatch.h>

#include "native_darwin.h"

static NSString *const MobileEgressRelayPlist = @"com.cbjjensen.mobile-egress.relay.plist";

static int mobileEgressBoundedStatus(SMAppServiceStatus status) {
    switch (status) {
        case SMAppServiceStatusNotRegistered:
            return 0;
        case SMAppServiceStatusRequiresApproval:
            return 1;
        case SMAppServiceStatusEnabled:
            return 2;
        case SMAppServiceStatusNotFound:
            return 3;
        default:
            return 4;
    }
}

int mobile_egress_relay_service_status(void) {
    if (@available(macOS 13.0, *)) {
        @autoreleasepool {
            SMAppService *service = [SMAppService daemonServiceWithPlistName:MobileEgressRelayPlist];
            int result = mobileEgressBoundedStatus(service.status);
            return result;
        }
    }
    return 4;
}

int mobile_egress_relay_service_register(void) {
    if (@available(macOS 13.0, *)) {
        @autoreleasepool {
            SMAppService *service = [SMAppService daemonServiceWithPlistName:MobileEgressRelayPlist];
            NSError *error = nil;
            if ([service registerAndReturnError:&error]) {
                return 0;
            }
            (void)error;
            switch (service.status) {
                case SMAppServiceStatusEnabled:
                case SMAppServiceStatusRequiresApproval:
                    return 1;
                case SMAppServiceStatusNotRegistered:
                    return 2;
                default:
                    return 4;
            }
        }
    }
    return 3;
}

int mobile_egress_relay_service_open_login_items(void) {
    if (@available(macOS 13.0, *)) {
        dispatch_async(dispatch_get_main_queue(), ^{
            @autoreleasepool {
                [SMAppService openSystemSettingsLoginItems];
            }
        });
        return 0;
    }
    return 3;
}
