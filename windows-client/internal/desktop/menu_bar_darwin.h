#ifndef MOBILE_EGRESS_MENU_BAR_DARWIN_H
#define MOBILE_EGRESS_MENU_BAR_DARWIN_H

#include <stddef.h>
#include <stdint.h>

void mobile_egress_menu_bar_start(
    const void *icon_bytes,
    size_t icon_length,
    const char *tooltip,
    const char *initial_status,
    const char *show_title,
    const char *show_tooltip,
    const char *quit_title,
    const char *quit_tooltip,
    uintptr_t callback_handle);
void mobile_egress_menu_bar_set_status(uintptr_t callback_handle, const char *status);
void mobile_egress_menu_bar_stop(uintptr_t callback_handle);

#ifdef MOBILE_EGRESS_MENU_BAR_CONTRACT
void mobile_egress_menu_bar_contract_run_application_loop(void);
void mobile_egress_menu_bar_contract_stop_application_loop(void);
void mobile_egress_menu_bar_contract_install_sentinel_delegate(void);
void mobile_egress_menu_bar_contract_remove_sentinel_delegate(void);
void mobile_egress_menu_bar_contract_reset_main_thread_checks(void);
int mobile_egress_menu_bar_contract_all_operations_on_main_thread(void);
int mobile_egress_menu_bar_contract_delegate_preserved(void);
int mobile_egress_menu_bar_contract_menu_present(void);
char *mobile_egress_menu_bar_contract_copy_status(void);
int mobile_egress_menu_bar_contract_click_show(void);
int mobile_egress_menu_bar_contract_click_quit(void);
#endif

#endif
