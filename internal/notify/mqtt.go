package notify

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	mqttMaxPayloadBytes = 256 * 1024 // 256 KiB hard ceiling per message
	mqttMaxTopicBytes   = 256
	mqttDefaultTimeout  = 15 * time.Second
)

// MQTTConfig describes an MQTT 3.1.1 broker connection.
type MQTTConfig struct {
	Endpoint  string // host:port
	TLS       bool
	TLSConfig *tls.Config // optional override (e.g. test CA)
	Username  string
	Password  string
	ClientID  string
}

// MQTTMessage is a single MQTT PUBLISH packet.
type MQTTMessage struct {
	Topic   string
	Payload []byte
	QoS     byte // 0, 1, or 2
	Retain  bool
}

// ValidateMQTTBroker checks that an endpoint is a valid host:port with no
// control characters.
func ValidateMQTTBroker(endpoint string) error {
	if endpoint == "" || strings.ContainsAny(endpoint, "\r\n\x00 ") {
		return errors.New("MQTT broker host:port required")
	}
	host, portStr, err := net.SplitHostPort(endpoint)
	if err != nil || strings.TrimSpace(host) == "" {
		return errors.New("MQTT broker must be host:port")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("MQTT broker port must be 1–65535")
	}
	return nil
}

// ValidateMQTTTopic ensures a publish topic is non-empty, bounded, and contains
// no MQTT wildcard or control characters.
func ValidateMQTTTopic(topic string) error {
	if topic == "" || len(topic) > mqttMaxTopicBytes {
		return fmt.Errorf("MQTT topic required (max %d bytes)", mqttMaxTopicBytes)
	}
	if strings.ContainsAny(topic, "+#\x00\r\n") {
		return errors.New("MQTT publish topic cannot contain wildcards or control characters")
	}
	return nil
}

func validateMQTTChannel(c Channel) error {
	if err := ValidateMQTTBroker(c.Secret("endpoint")); err != nil {
		return err
	}
	mode := c.Setting("tlsMode")
	if mode != "" && mode != "none" && mode != "tls" {
		return errors.New("MQTT TLS mode must be none or tls")
	}
	if err := ValidateMQTTTopic(c.Setting("topic")); err != nil {
		return err
	}
	if c.Secret("password") != "" && c.Secret("username") == "" {
		return errors.New("MQTT username required when password is set")
	}
	qos := c.Setting("qos")
	if qos != "" && qos != "0" && qos != "1" && qos != "2" {
		return errors.New("MQTT QoS must be 0, 1, or 2")
	}
	if cid := c.Setting("clientId"); len(cid) > 128 || strings.ContainsAny(cid, "\r\n\x00") {
		return errors.New("invalid MQTT client ID")
	}
	return nil
}

// deliverMQTT delivers a notify.Event as JSON to the channel's configured MQTT broker and topic.
func deliverMQTT(ctx context.Context, _ *Service, c Channel, e Event) error {
	payload, err := json.Marshal(e)
	if err != nil {
		return errors.New("encode notification failed")
	}
	qos := byte(0)
	if q, err := strconv.Atoi(c.Setting("qos")); err == nil && q >= 0 && q <= 2 {
		qos = byte(q)
	}
	cfg := mqttConfigFromChannel(c)
	return PublishMQTT(ctx, cfg, MQTTMessage{
		Topic:   c.Setting("topic"),
		Payload: payload,
		QoS:     qos,
	})
}

func mqttConfigFromChannel(c Channel) MQTTConfig {
	endpoint := c.Secret("endpoint")
	_, port, _ := net.SplitHostPort(endpoint)
	mode := c.Setting("tlsMode")
	useTLS := mode == "tls" || (mode == "" && port == "8883")
	return MQTTConfig{
		Endpoint: endpoint,
		TLS:      useTLS,
		Username: c.Secret("username"),
		Password: c.Secret("password"),
		ClientID: c.Setting("clientId"),
	}
}

// PublishMQTT looks up an enabled MQTT notification channel by brokerID, opens
// its sealed broker credentials, and publishes one MQTT 3.1.1 message.
func (s *Service) PublishMQTT(ctx context.Context, brokerID, topic string, payload []byte, qos byte, retain bool) error {
	if s == nil {
		return errors.New("notification service unavailable")
	}
	if strings.TrimSpace(brokerID) == "" {
		return errors.New("MQTT broker channel ID required")
	}
	if err := ValidateMQTTTopic(topic); err != nil {
		return err
	}
	cs, err := s.channels(ctx)
	if err != nil {
		return errors.New("could not load MQTT broker configuration")
	}
	var found *Channel
	for i := range cs {
		if cs[i].ID == brokerID {
			found = &cs[i]
			break
		}
	}
	if found == nil {
		return fmt.Errorf("MQTT broker channel %q not found", brokerID)
	}
	if found.Kind != "mqtt" {
		return fmt.Errorf("channel %q is kind %q, not mqtt", brokerID, found.Kind)
	}
	if !found.Enabled {
		return fmt.Errorf("MQTT broker channel %q is disabled", brokerID)
	}
	return PublishMQTT(ctx, mqttConfigFromChannel(*found), MQTTMessage{
		Topic:   topic,
		Payload: payload,
		QoS:     qos,
		Retain:  retain,
	})
}

// PublishMQTT opens a bounded MQTT 3.1.1 connection, publishes msg with the
// requested QoS (0, 1, or 2) and retain flag, and disconnects cleanly.
// Returned errors never include broker addresses, usernames, or passwords.
func PublishMQTT(ctx context.Context, cfg MQTTConfig, msg MQTTMessage) error {
	if err := ValidateMQTTBroker(cfg.Endpoint); err != nil {
		return err
	}
	if err := ValidateMQTTTopic(msg.Topic); err != nil {
		return err
	}
	if msg.QoS > 2 {
		return errors.New("MQTT QoS must be 0, 1, or 2")
	}
	if len(msg.Payload) > mqttMaxPayloadBytes {
		return fmt.Errorf("MQTT payload exceeds %d bytes", mqttMaxPayloadBytes)
	}
	host, _, err := net.SplitHostPort(cfg.Endpoint)
	if err != nil {
		return errors.New("invalid MQTT broker address")
	}

	dialer := net.Dialer{Timeout: mqttDefaultTimeout}
	var conn net.Conn
	if cfg.TLS {
		tlsCfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
		if cfg.TLSConfig != nil {
			tlsCfg = cfg.TLSConfig.Clone()
			if tlsCfg.ServerName == "" {
				tlsCfg.ServerName = host
			}
		}
		conn, err = (&tls.Dialer{NetDialer: &dialer, Config: tlsCfg}).DialContext(ctx, "tcp", cfg.Endpoint)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", cfg.Endpoint)
	}
	if err != nil {
		return errors.New("MQTT broker connection failed")
	}
	defer conn.Close()

	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	deadline := time.Now().Add(mqttDefaultTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)

	clientID := strings.TrimSpace(cfg.ClientID)
	if clientID == "" {
		var rnd [6]byte
		_, _ = rand.Read(rnd[:])
		clientID = "ether-" + hex.EncodeToString(rnd[:])
	}

	// 1. CONNECT
	if _, err := conn.Write(encodeMQTTConnect(clientID, cfg.Username, cfg.Password, 30)); err != nil {
		return errors.New("MQTT connect write failed")
	}
	r := bufio.NewReaderSize(conn, 4096)
	pktType, _, body, err := readMQTTPacket(r)
	if err != nil {
		return errors.New("MQTT connect acknowledgement failed")
	}
	if pktType != 0x02 || len(body) != 2 {
		return errors.New("unexpected MQTT response to CONNECT")
	}
	if body[1] != 0x00 {
		return mqttConnackError(body[1])
	}

	// 2. PUBLISH
	var pktID uint16
	if msg.QoS > 0 {
		pktID = 1
	}
	if _, err := conn.Write(encodeMQTTPublish(msg.Topic, msg.Payload, msg.QoS, msg.Retain, pktID)); err != nil {
		return errors.New("MQTT publish write failed")
	}

	// 3. QoS 1 / QoS 2 handshake
	switch msg.QoS {
	case 1:
		pt, _, ack, err := readMQTTPacket(r)
		if err != nil || pt != 0x04 || len(ack) != 2 || binary.BigEndian.Uint16(ack) != pktID {
			return errors.New("MQTT PUBACK failed")
		}
	case 2:
		pt, _, rec, err := readMQTTPacket(r)
		if err != nil || pt != 0x05 || len(rec) != 2 || binary.BigEndian.Uint16(rec) != pktID {
			return errors.New("MQTT PUBREC failed")
		}
		// PUBREL fixed header is 0x62 (type 6, flags 0010), remaining length 2, packet ID
		rel := []byte{0x62, 0x02, byte(pktID >> 8), byte(pktID)}
		if _, err := conn.Write(rel); err != nil {
			return errors.New("MQTT PUBREL write failed")
		}
		pt, _, comp, err := readMQTTPacket(r)
		if err != nil || pt != 0x07 || len(comp) != 2 || binary.BigEndian.Uint16(comp) != pktID {
			return errors.New("MQTT PUBCOMP failed")
		}
	}

	// 4. DISCONNECT (best-effort)
	_, _ = conn.Write([]byte{0xE0, 0x00})
	return nil
}

func mqttConnackError(code byte) error {
	switch code {
	case 1:
		return errors.New("MQTT broker rejected protocol version")
	case 2:
		return errors.New("MQTT broker rejected client identifier")
	case 3:
		return errors.New("MQTT broker unavailable")
	case 4:
		return errors.New("MQTT authentication failed")
	case 5:
		return errors.New("MQTT client not authorized")
	default:
		return fmt.Errorf("MQTT connection refused (code %d)", code)
	}
}

func encodeMQTTConnect(clientID, username, password string, keepAliveSec uint16) []byte {
	var body bytes.Buffer
	// Protocol Name "MQTT" + Level 4 (3.1.1)
	writeMQTTString(&body, "MQTT")
	body.WriteByte(0x04)

	var flags byte = 0x02 // Clean Session
	if username != "" {
		flags |= 0x80
	}
	if password != "" {
		flags |= 0x40
	}
	body.WriteByte(flags)
	_ = binary.Write(&body, binary.BigEndian, keepAliveSec)

	writeMQTTString(&body, clientID)
	if username != "" {
		writeMQTTString(&body, username)
	}
	if password != "" {
		writeMQTTString(&body, password)
	}
	return frameMQTTPacket(0x10, body.Bytes())
}

func encodeMQTTPublish(topic string, payload []byte, qos byte, retain bool, pktID uint16) []byte {
	var header byte = 0x30 | ((qos & 0x03) << 1)
	if retain {
		header |= 0x01
	}
	var body bytes.Buffer
	writeMQTTString(&body, topic)
	if qos > 0 {
		_ = binary.Write(&body, binary.BigEndian, pktID)
	}
	body.Write(payload)
	return frameMQTTPacket(header, body.Bytes())
}

func writeMQTTString(b *bytes.Buffer, s string) {
	_ = binary.Write(b, binary.BigEndian, uint16(len(s)))
	b.WriteString(s)
}

func frameMQTTPacket(header byte, body []byte) []byte {
	rem := encodeMQTTRemainingLength(len(body))
	out := make([]byte, 0, 1+len(rem)+len(body))
	out = append(out, header)
	out = append(out, rem...)
	out = append(out, body...)
	return out
}

func encodeMQTTRemainingLength(n int) []byte {
	var out [4]byte
	i := 0
	for {
		encoded := byte(n % 128)
		n /= 128
		if n > 0 {
			encoded |= 0x80
		}
		out[i] = encoded
		i++
		if n == 0 || i == 4 {
			break
		}
	}
	return out[:i]
}

func readMQTTPacket(r *bufio.Reader) (pktType byte, flags byte, body []byte, err error) {
	first, err := r.ReadByte()
	if err != nil {
		return 0, 0, nil, err
	}
	pktType = first >> 4
	flags = first & 0x0F

	remLen, err := readMQTTRemainingLength(r)
	if err != nil {
		return 0, 0, nil, err
	}
	if remLen > 64*1024 {
		return 0, 0, nil, errors.New("MQTT control packet too large")
	}
	body = make([]byte, remLen)
	if _, err := io.ReadFull(r, body); err != nil {
		return 0, 0, nil, err
	}
	return pktType, flags, body, nil
}

func readMQTTRemainingLength(r *bufio.Reader) (int, error) {
	multiplier := 1
	value := 0
	for i := 0; i < 4; i++ {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		value += int(b&0x7F) * multiplier
		if b&0x80 == 0 {
			return value, nil
		}
		multiplier *= 128
	}
	return 0, errors.New("malformed MQTT remaining length")
}
