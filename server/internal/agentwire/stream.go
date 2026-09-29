package agentwire

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	ProtocolVersion = 1
	MaxStreams      = 128
	frameData       = byte(1)
	frameFIN        = byte(2)
	frameReset      = byte(3)
)

// Bridge carries an unmodified Docker HTTP connection, including hijacked
// connections, across one WebSocket. FIN preserves TCP half-close semantics.
func Bridge(ctx context.Context, socket net.Conn, ws *websocket.Conn) error {
	defer socket.Close()
	defer ws.Close()
	ws.SetReadLimit(64 << 10)
	var writeMu sync.Mutex
	write := func(kind byte, body []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return ws.WriteMessage(websocket.BinaryMessage, append([]byte{kind}, body...))
	}
	writeSocket := func(value []byte) error {
		for len(value) > 0 {
			n, err := socket.Write(value)
			if err != nil {
				return err
			}
			if n == 0 {
				return io.ErrShortWrite
			}
			value = value[n:]
		}
		return nil
	}
	result := make(chan error, 2)
	go func() {
		buffer := make([]byte, 32<<10)
		for {
			n, err := socket.Read(buffer)
			if n > 0 {
				if writeErr := write(frameData, buffer[:n]); writeErr != nil {
					result <- writeErr
					return
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					result <- write(frameFIN, nil)
				} else {
					_ = write(frameReset, nil)
					result <- err
				}
				return
			}
		}
	}()
	go func() {
		for {
			kind, payload, err := ws.ReadMessage()
			if err != nil {
				result <- err
				return
			}
			if kind != websocket.BinaryMessage || len(payload) == 0 {
				result <- errors.New("invalid agent stream frame")
				return
			}
			switch payload[0] {
			case frameData:
				if len(payload) == 1 {
					continue
				}
				if err := writeSocket(payload[1:]); err != nil {
					result <- err
					return
				}
			case frameFIN:
				if half, ok := socket.(interface{ CloseWrite() error }); ok {
					_ = half.CloseWrite()
				}
				result <- nil
				return
			case frameReset:
				result <- errors.New("agent stream reset")
				return
			default:
				result <- errors.New("unknown agent stream frame")
				return
			}
		}
	}()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	completed := 0
	for completed < 2 {
		select {
		case err := <-result:
			if err != nil {
				return err
			}
			completed++
		case <-ping.C:
			if err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
