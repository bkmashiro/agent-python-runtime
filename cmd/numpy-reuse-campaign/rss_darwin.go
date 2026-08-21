//go:build darwin && cgo

package main

/*
#include <mach/mach.h>
#include <stdint.h>
static uint64_t pysolate_current_rss(void) {
    mach_task_basic_info_data_t info;
    mach_msg_type_number_t count = MACH_TASK_BASIC_INFO_COUNT;
    kern_return_t status = task_info(mach_task_self(), MACH_TASK_BASIC_INFO, (task_info_t)&info, &count);
    if (status != KERN_SUCCESS) return 0;
    return (uint64_t)info.resident_size;
}
*/
import "C"

func currentRSSBytes() uint64 { return uint64(C.pysolate_current_rss()) }
