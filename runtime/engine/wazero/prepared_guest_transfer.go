package wazero

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/tetratelabs/wazero/api"
)

var ErrPreparedGuestTransfer = errors.New("prepared numpy Guest transfer failed")

type preparedGuestTransfer interface {
	allocate(context.Context, uint32) (uint32, error)
	write(uint32, []byte) bool
	call(context.Context, uint32, uint32, uint32, uint32) error
	deallocate(uint32)
}

type modulePreparedGuestTransfer struct {
	module api.Module
}

func (transfer modulePreparedGuestTransfer) allocate(ctx context.Context, size uint32) (uint32, error) {
	function := transfer.module.ExportedFunction("alloc")
	if function == nil {
		return 0, ErrPreparedGuestTransfer
	}
	result, err := function.Call(ctx, uint64(size))
	if err != nil || len(result) != 1 || result[0] == 0 || result[0] > math.MaxUint32 {
		return 0, fmt.Errorf("%w: allocation", ErrPreparedGuestTransfer)
	}
	return uint32(result[0]), nil
}

func (transfer modulePreparedGuestTransfer) write(pointer uint32, value []byte) bool {
	return transfer.module.Memory().Write(pointer, value)
}

func (transfer modulePreparedGuestTransfer) call(ctx context.Context, descriptorPointer, descriptorLength, bodyPointer, bodyLength uint32) error {
	function := transfer.module.ExportedFunction("runtime_prepare_numpy_ndarray")
	if function == nil {
		return fmt.Errorf("%w: required export is missing", ErrPreparedGuestTransfer)
	}
	result, err := function.Call(ctx, uint64(descriptorPointer), uint64(descriptorLength), uint64(bodyPointer), uint64(bodyLength))
	if err != nil || len(result) != 1 || uint32(result[0]) != 0 {
		return fmt.Errorf("%w: preparation call", ErrPreparedGuestTransfer)
	}
	return nil
}

func (transfer modulePreparedGuestTransfer) deallocate(pointer uint32) {
	function := transfer.module.ExportedFunction("dealloc")
	if function == nil || pointer == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _ = function.Call(ctx, uint64(pointer))
}

func transferPreparedNumpyInput(ctx context.Context, transfer preparedGuestTransfer, descriptor, body []byte) error {
	if transfer == nil || len(descriptor) == 0 || len(descriptor) > 4096 || len(body) == 0 || len(body) > 8*1024*1024 {
		return ErrPreparedGuestTransfer
	}
	descriptorPointer, err := transfer.allocate(ctx, uint32(len(descriptor)))
	if err != nil {
		return err
	}
	defer transfer.deallocate(descriptorPointer)
	bodyPointer, err := transfer.allocate(ctx, uint32(len(body)))
	if err != nil {
		return err
	}
	defer transfer.deallocate(bodyPointer)
	if !transfer.write(descriptorPointer, descriptor) || !transfer.write(bodyPointer, body) {
		return fmt.Errorf("%w: short Guest memory write", ErrPreparedGuestTransfer)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return transfer.call(ctx, descriptorPointer, uint32(len(descriptor)), bodyPointer, uint32(len(body)))
}

func callPreparedNumpyInput(ctx context.Context, module api.Module, input PreparedNumpyInput) error {
	if module == nil || input.identity == "" {
		return ErrPreparedGuestTransfer
	}
	return transferPreparedNumpyInput(ctx, modulePreparedGuestTransfer{module: module}, input.descriptorJSON, input.body)
}
