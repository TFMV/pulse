package iso

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"time"

	"github.com/moov-io/iso8583"
	"github.com/moov-io/iso8583/encoding"
	"github.com/moov-io/iso8583/field"
	"github.com/moov-io/iso8583/prefix"
)

// Server represents the ISO8583 TCP server
type Server struct {
	address     string
	handler     MessageHandler
	listener    net.Listener
	isShutdown  bool
	connections map[string]net.Conn
	spec        *iso8583.MessageSpec
}

// MessageHandler defines the interface for handling ISO8583 messages
type MessageHandler interface {
	HandleMessage(ctx context.Context, message *iso8583.Message) (*iso8583.Message, error)
}

// NewServer creates a new ISO8583 TCP server
func NewServer(address string, handler MessageHandler) *Server {
	// Create a default ISO8583 message spec for the server
	spec := &iso8583.MessageSpec{
		Name: "ISO 8583 v1987",
		Fields: map[int]field.Field{
			0:  field.NewString(field.NewSpec(4, "Message Type Indicator", encoding.ASCII, prefix.None.Fixed)),
			2:  field.NewString(field.NewSpec(19, "Primary Account Number", encoding.ASCII, prefix.None.Fixed)),
			4:  field.NewString(field.NewSpec(12, "Amount, Transaction", encoding.ASCII, prefix.None.Fixed)),
			7:  field.NewString(field.NewSpec(10, "Transmission Date and Time", encoding.ASCII, prefix.None.Fixed)),
			11: field.NewString(field.NewSpec(6, "System Trace Audit Number", encoding.ASCII, prefix.None.Fixed)),
			39: field.NewString(field.NewSpec(2, "Response Code", encoding.ASCII, prefix.None.Fixed)),
		},
	}

	return &Server{
		address:     address,
		handler:     handler,
		connections: make(map[string]net.Conn),
		spec:        spec,
	}
}

// Start starts the ISO8583 TCP server
func (s *Server) Start() error {
	var err error
	s.listener, err = net.Listen("tcp", s.address)
	if err != nil {
		return fmt.Errorf("failed to start TCP server: %w", err)
	}

	log.Printf("ISO8583 server listening on %s", s.address)

	for !s.isShutdown {
		conn, err := s.listener.Accept()
		if err != nil {
			if s.isShutdown {
				return nil
			}
			log.Printf("Error accepting connection: %v", err)
			continue
		}

		clientAddr := conn.RemoteAddr().String()
		s.connections[clientAddr] = conn
		log.Printf("New connection from %s", clientAddr)

		go s.handleConnection(conn)
	}

	return nil
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown() error {
	s.isShutdown = true

	if s.listener != nil {
		if err := s.listener.Close(); err != nil {
			return err
		}
	}

	// Close all active connections
	for addr, conn := range s.connections {
		log.Printf("Closing connection from %s", addr)
		conn.Close()
		delete(s.connections, addr)
	}

	return nil
}

// handleConnection handles a single client connection
func (s *Server) handleConnection(conn net.Conn) {
	defer func() {
		conn.Close()
		clientAddr := conn.RemoteAddr().String()
		delete(s.connections, clientAddr)
		log.Printf("Connection from %s closed", clientAddr)
	}()

	reader := bufio.NewReader(conn)
	for {
		// Handle potential timeout
		if err := conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
			log.Printf("Error setting read deadline: %v", err)
			return
		}

		// Read message length (2 bytes)
		lengthBytes := make([]byte, 2)
		if _, err := io.ReadFull(reader, lengthBytes); err != nil {
			if err == io.EOF {
				return
			}
			log.Printf("Error reading message length: %v", err)
			return
		}

		// Convert length bytes to int
		messageLength := int(lengthBytes[0])*256 + int(lengthBytes[1])

		// Read the ISO message
		messageBytes := make([]byte, messageLength)
		if _, err := io.ReadFull(reader, messageBytes); err != nil {
			log.Printf("Error reading message body: %v", err)
			return
		}

		// Process the message
		if err := s.processMessage(conn, messageBytes); err != nil {
			log.Printf("Error processing message: %v", err)
			return
		}
	}
}

// processMessage processes an ISO8583 message and sends a response
func (s *Server) processMessage(conn net.Conn, messageBytes []byte) error {
	start := time.Now()

	// Parse the ISO message
	message := iso8583.NewMessage(s.spec)
	if err := message.Unpack(messageBytes); err != nil {
		log.Printf("Error unpacking ISO message: %v", err)
		return err
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Pass to handler
	responseMessage, err := s.handler.HandleMessage(ctx, message)
	if err != nil {
		log.Printf("Error handling message: %v", err)
		return err
	}

	// Pack the response
	responseBytes, err := responseMessage.Pack()
	if err != nil {
		log.Printf("Error packing response message: %v", err)
		return err
	}

	// Create length-prefixed message
	responseLengthBytes := []byte{byte(len(responseBytes) / 256), byte(len(responseBytes) % 256)}
	fullResponse := append(responseLengthBytes, responseBytes...)

	// Send the response
	if _, err := conn.Write(fullResponse); err != nil {
		log.Printf("Error writing response: %v", err)
		return err
	}

	elapsed := time.Since(start)
	log.Printf("Message processed in %s", elapsed)

	return nil
}
