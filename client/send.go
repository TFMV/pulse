package client

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/moov-io/iso8583"
	"github.com/moov-io/iso8583/encoding"
	"github.com/moov-io/iso8583/field"
	"github.com/moov-io/iso8583/prefix"
)

// Client represents an ISO8583 client
type Client struct {
	serverAddr  string
	conn        net.Conn
	messageSpec *iso8583.MessageSpec
}

// NewClient creates a new ISO8583 client
func NewClient(serverAddr string) *Client {
	return &Client{
		serverAddr: serverAddr,
	}
}

// Connect establishes a connection to the server
func (c *Client) Connect() error {
	var err error
	c.conn, err = net.Dial("tcp", c.serverAddr)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", c.serverAddr, err)
	}

	// Initialize ISO8583 message spec
	c.messageSpec = &iso8583.MessageSpec{
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

	return nil
}

// SendAuthRequest sends an authorization request and returns the response
func (c *Client) SendAuthRequest(pan, amount string) (*iso8583.Message, error) {
	// Create the ISO8583 message
	message := iso8583.NewMessage(c.messageSpec)

	// Set MTI (Field 0)
	if err := message.Field(0, "0100"); err != nil {
		return nil, fmt.Errorf("failed to set MTI: %w", err)
	}

	// Set PAN (Field 2)
	if err := message.Field(2, pan); err != nil {
		return nil, fmt.Errorf("failed to set PAN: %w", err)
	}

	// Set Amount (Field 4)
	if err := message.Field(4, amount); err != nil {
		return nil, fmt.Errorf("failed to set amount: %w", err)
	}

	// Set Transaction Time (Field 7)
	now := time.Now()
	transmissionTime := now.Format("0102150405") // MMDDhhmmss
	if err := message.Field(7, transmissionTime); err != nil {
		return nil, fmt.Errorf("failed to set transmission time: %w", err)
	}

	// Set STAN (Field 11)
	stan := fmt.Sprintf("%06d", now.Unix()%1000000)
	if err := message.Field(11, stan); err != nil {
		return nil, fmt.Errorf("failed to set STAN: %w", err)
	}

	// Pack the message
	packed, err := message.Pack()
	if err != nil {
		return nil, fmt.Errorf("failed to pack message: %w", err)
	}

	// Add length prefix (2 bytes)
	length := len(packed)
	lengthBytes := []byte{byte(length / 256), byte(length % 256)}
	fullMessage := append(lengthBytes, packed...)

	// Send the message
	start := time.Now()
	_, err = c.conn.Write(fullMessage)
	if err != nil {
		return nil, fmt.Errorf("failed to send message: %w", err)
	}

	// Read the response
	response, err := c.ReadResponse()
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	elapsed := time.Since(start)
	log.Printf("Transaction completed in %s", elapsed)

	return response, nil
}

// ReadResponse reads an ISO8583 response
func (c *Client) ReadResponse() (*iso8583.Message, error) {
	reader := bufio.NewReader(c.conn)

	// Read length bytes (2 bytes)
	lengthBytes := make([]byte, 2)
	if _, err := io.ReadFull(reader, lengthBytes); err != nil {
		return nil, fmt.Errorf("failed to read length bytes: %w", err)
	}

	// Convert to int
	length := int(lengthBytes[0])*256 + int(lengthBytes[1])

	// Read the message
	messageBytes := make([]byte, length)
	if _, err := io.ReadFull(reader, messageBytes); err != nil {
		return nil, fmt.Errorf("failed to read message: %w", err)
	}

	// Unpack the message
	message := iso8583.NewMessage(c.messageSpec)
	if err := message.Unpack(messageBytes); err != nil {
		return nil, fmt.Errorf("failed to unpack message: %w", err)
	}

	return message, nil
}

// Close closes the connection
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// PrintResponse prints the response in a human-readable format
func PrintResponse(message *iso8583.Message) {
	mti, _ := message.GetString(0)

	fmt.Println("=== ISO8583 Response ===")
	fmt.Printf("MTI: %s\n", mti)

	// Print common fields
	fields := []struct {
		id   int
		name string
	}{
		{2, "PAN"},
		{4, "Amount"},
		{7, "Transmission Time"},
		{11, "STAN"},
		{39, "Response Code"},
	}

	for _, field := range fields {
		if value, err := message.GetString(field.id); err == nil {

			// Mask PAN for display
			if field.id == 2 {
				if len(value) > 10 {
					value = value[:6] + "******" + value[len(value)-4:]
				}
			}

			// Format the response code
			if field.id == 39 {
				value = fmt.Sprintf("%s (%s)", value, getResponseCodeDescription(value))
			}

			fmt.Printf("%s: %s\n", field.name, value)
		}
	}

	fmt.Println("========================")
}

// getResponseCodeDescription returns a description for a response code
func getResponseCodeDescription(code string) string {
	codes := map[string]string{
		"00": "Approved",
		"01": "Refer to card issuer",
		"02": "Refer to card issuer, special condition",
		"03": "Invalid merchant",
		"04": "Pick up card",
		"05": "Do not honor",
		"06": "Error",
		"07": "Pick up card, special condition",
		"08": "Honor with identification",
		"09": "Request in progress",
		"10": "Approved, partial",
		"11": "Approved, VIP",
		"12": "Invalid transaction",
		"13": "Invalid amount",
		"14": "Invalid card number",
		"15": "No such issuer",
		"51": "Insufficient funds",
		"54": "Expired card",
		"55": "Invalid PIN",
		"59": "Suspected fraud",
		"91": "Issuer or switch inoperative",
		"96": "System malfunction",
	}

	if desc, ok := codes[code]; ok {
		return desc
	}
	return "Unknown"
}

// RunInteractiveClient runs an interactive client session
func RunInteractiveClient(serverAddr string) error {
	client := NewClient(serverAddr)
	if err := client.Connect(); err != nil {
		return err
	}
	defer client.Close()

	fmt.Println("Connected to Pulse server at", serverAddr)
	fmt.Println("Enter transactions (PAN,Amount) or 'quit' to exit.")
	fmt.Println("Examples:")
	fmt.Println("  4111111111111111,50.00  - US transaction, should be approved")
	fmt.Println("  4111111111111110,50.00  - US transaction with PAN ending in 0, should be declined")
	fmt.Println("  4111111111111111,550.00 - US transaction exceeding limit, should be declined")
	fmt.Println("  5555555555554444,100.00 - EU transaction, should be approved")
	fmt.Println("  5555555555554444,450.00 - EU transaction exceeding limit, should be declined")

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}

		input := scanner.Text()
		if input == "quit" || input == "exit" {
			break
		}

		if input == "" {
			continue
		}

		parts := strings.Split(input, ",")
		if len(parts) != 2 {
			fmt.Println("Invalid input. Use format: PAN,Amount")
			continue
		}

		pan := strings.TrimSpace(parts[0])
		amount := strings.TrimSpace(parts[1])

		// Convert amount to cents
		amountFloat, err := strconv.ParseFloat(amount, 64)
		if err != nil {
			fmt.Println("Invalid amount:", err)
			continue
		}

		// Format amount for ISO8583 (no decimal point, 12 digits)
		amountCents := fmt.Sprintf("%012d", int(amountFloat*100))

		fmt.Printf("Sending transaction: PAN=%s, Amount=$%s\n",
			pan[:6]+"******"+pan[len(pan)-4:], amount)

		response, err := client.SendAuthRequest(pan, amountCents)
		if err != nil {
			fmt.Println("Error:", err)
			continue
		}

		PrintResponse(response)
	}

	return nil
}
