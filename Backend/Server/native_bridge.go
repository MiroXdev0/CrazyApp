package main

/*
#cgo CXXFLAGS: -std=c++17 -I. -I../../Core/C/header -I../../Core/Cpp/header
#include <stdlib.h>
#include "native_task_bridge.h"
*/
import "C"

import (
	"fmt"
	"unsafe"
)

type NativeExecutionHandle struct {
	ptr unsafe.Pointer
}

func newNativeExecutionHandle() (*NativeExecutionHandle, error) {
	ptr := C.nodren_native_create_engine()
	if ptr == nil {
		return nil, fmt.Errorf("native execution engine unavailable")
	}
	return &NativeExecutionHandle{ptr: ptr}, nil
}

func (h *NativeExecutionHandle) Close() {
	if h == nil || h.ptr == nil {
		return
	}
	C.nodren_native_destroy_engine(h.ptr)
	h.ptr = nil
}

func taskPayloadToInt32(payload []byte) []int32 {
	if len(payload) == 0 {
		return nil
	}
	values := make([]int32, len(payload))
	for i, value := range payload {
		values[i] = int32(value)
	}
	return values
}

func (h *NativeExecutionHandle) SubmitTask(task TaskPacket) (uint64, error) {
	if h == nil || h.ptr == nil {
		return 0, fmt.Errorf("native execution engine unavailable")
	}
	jobID := task.JobID
	if jobID == "" {
		jobID = fmt.Sprintf("job-%d", task.TaskID)
	}
	cTaskID := C.CString(jobID)
	defer C.free(unsafe.Pointer(cTaskID))

	values := taskPayloadToInt32(task.Payload)
	if len(values) == 0 {
		return 0, fmt.Errorf("native task payload is empty")
	}
	result := C.nodren_native_submit_task(h.ptr, cTaskID, (*C.int32_t)(unsafe.Pointer(&values[0])), C.size_t(len(values)))
	if result < 0 {
		return 0, fmt.Errorf("native task execution failed for task %d", task.TaskID)
	}
	return uint64(result), nil
}

func ExecuteNativeTaskBatch(tasks []TaskPacket) ([]ResultPacket, error) {
	if len(tasks) == 0 {
		return nil, nil
	}
	handle, err := newNativeExecutionHandle()
	if err != nil {
		return nil, err
	}
	defer handle.Close()

	results := make([]ResultPacket, 0, len(tasks))
	for _, task := range tasks {
		jobID := task.JobID
		if jobID == "" {
			jobID = fmt.Sprintf("job-%d", task.TaskID)
		}
		resultValue, err := handle.SubmitTask(task)
		if err != nil {
			return nil, err
		}
		results = append(results, ResultPacket{TaskID: task.TaskID, JobID: jobID, Result: resultValue})
	}
	return results, nil
}
