package hermesbridge

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"time"
)

type Server struct {
	service        *Service
	maxConcurrency uint32
	connectionTTL  time.Duration
}

func NewServer(service *Service, maxConcurrency uint32, connectionTTL time.Duration) (*Server, error) {
	if service == nil || maxConcurrency == 0 || maxConcurrency > 64 || connectionTTL <= 0 || connectionTTL > 5*time.Minute {
		return nil, errors.New("invalid Hermes bridge server")
	}
	return &Server{service: service, maxConcurrency: maxConcurrency, connectionTTL: connectionTTL}, nil
}

func (server *Server) Serve(ctx context.Context, listener net.Listener) error {
	if server == nil || listener == nil || ctx == nil {
		return errors.New("invalid Hermes bridge listener")
	}
	closed := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = listener.Close()
		case <-closed:
		}
	}()
	defer close(closed)
	semaphore := make(chan struct{}, server.maxConcurrency)
	var workers sync.WaitGroup
	defer workers.Wait()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case semaphore <- struct{}{}:
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer func() { <-semaphore }()
				server.handle(ctx, connection)
			}()
		case <-ctx.Done():
			_ = connection.Close()
			return nil
		}
	}
}

func (server *Server) handle(ctx context.Context, connection net.Conn) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(server.connectionTTL))
	payload, err := ReadFrame(connection, MaxFrameBytes)
	if err != nil {
		server.writeError(connection, "unknown", "invalid_frame", "invalid bridge frame")
		return
	}
	request, err := DecodeExecuteRequest(payload)
	if err != nil {
		server.writeError(connection, "unknown", "invalid_request", "invalid runtime request")
		return
	}
	response := server.service.Execute(ctx, request)
	encoded, err := json.Marshal(response)
	if err != nil || WriteFrame(connection, encoded, MaxFrameBytes) != nil {
		return
	}
}

func (server *Server) writeError(connection net.Conn, requestID, code, message string) {
	response := ExecuteResponse{
		Version: ProtocolVersion, RequestID: requestID, Status: ResponseStatusError,
		Error: &BridgeError{Code: code, Message: message},
	}
	encoded, err := json.Marshal(response)
	if err == nil {
		_ = WriteFrame(connection, encoded, MaxFrameBytes)
	}
}
