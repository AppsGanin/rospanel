package xray

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"
)

// xrayAPI makes the calls to the running Xray's gRPC API that the `xray api` CLI cannot.
//
// The CLI refuses to add a user to a Hysteria2 inbound ("unsupported inbound type": its
// list of inbound types predates the protocol), although the inbound itself takes users
// through the API like any other — verified against Xray 26.7.28. And the CLI runs all
// of one command's calls inside a single three-second deadline, which removing many
// users from a QUIC inbound outlasts, since that inbound finds a user by email by walking
// all of them: of 8,000 removals from an inbound of 50,000 users the CLI made 4,667,
// printed a timeout for each of the rest and exited 0.
//
// The two messages involved are small and fixed, so they are encoded here rather than
// bringing in Xray's generated protobuf and a gRPC client.
type xrayAPI struct {
	addr   string
	client *http.Client
	wait   func(time.Duration)
}

// apiCallTimeout bounds one call. A removal walks the inbound's users — half a
// millisecond at fifty thousand on a laptop; the rest is headroom for a loaded box.
const apiCallTimeout = 10 * time.Second

const (
	handlerAlterInbound = "/xray.app.proxyman.command.HandlerService/AlterInbound"
	statsQueryStats     = "/xray.app.stats.command.StatsService/QueryStats"
)

// apiMaxAnswer bounds the answer read back: every per-user counter of a large panel is
// a few megabytes.
const apiMaxAnswer = 64 << 20

// apiMaxMessage bounds the message one call sends. A gRPC frame carries the length in
// four bytes, and Xray, like any grpc-go server, refuses a message over 4 MiB by
// default; the messages here — one user at a time — are a few hundred bytes.
const apiMaxMessage = 4 << 20

func newXrayAPI(addr string, wait func(time.Duration)) *xrayAPI {
	// gRPC is HTTP/2; Xray's API listens without TLS, so the client speaks HTTP/2 from
	// the first byte.
	var protocols http.Protocols
	protocols.SetUnencryptedHTTP2(true)
	return &xrayAPI{
		addr:   addr,
		client: &http.Client{Transport: &http.Transport{Protocols: &protocols}},
		wait:   wait,
	}
}

func (a *xrayAPI) close() { a.client.CloseIdleConnections() }

// addHysteriaUser adds one user to a Hysteria2 inbound. Adding a user it already holds
// replaces the entry: the inbound keys its users by their auth.
func (a *xrayAPI) addHysteriaUser(tag, email, auth string) error {
	if email == "" || auth == "" {
		return fmt.Errorf("hysteria user %q with an empty email or auth", email)
	}
	account := pbField(nil, 1, []byte(auth)) // xray.proxy.hysteria.account.Account.auth
	user := pbField(nil, 2, []byte(email))   // xray.common.protocol.User.email (level 0 is the default)
	user = pbField(user, 3, typedMessage("xray.proxy.hysteria.account.Account", account))
	op := typedMessage("xray.app.proxyman.command.AddUserOperation", pbField(nil, 1, user))
	if _, err := a.call(handlerAlterInbound, alterInbound(tag, op)); err != nil {
		return fmt.Errorf("add %s to %s: %w", email, tag, err)
	}
	return nil
}

// addWireGuardUser adds one peer to a WireGuard inbound. The CLI refuses this inbound
// type too. The key goes in as hex: Xray's config parser turns a base64 key into hex
// before the inbound sees it, and the account the API hands the inbound is read the same
// way, so base64 here is refused as a malformed key.
func (a *xrayAPI) addWireGuardUser(tag, email, publicKeyHex string, allowedIPs []string) error {
	if email == "" || publicKeyHex == "" {
		return fmt.Errorf("wireguard user %q with an empty email or key", email)
	}
	account := pbField(nil, 1, []byte(publicKeyHex)) // xray.proxy.wireguard.PeerConfig.public_key
	for _, ip := range allowedIPs {
		account = pbField(account, 5, []byte(ip)) // xray.proxy.wireguard.PeerConfig.allowed_ips
	}
	user := pbField(nil, 2, []byte(email))
	user = pbField(user, 3, typedMessage("xray.proxy.wireguard.PeerConfig", account))
	op := typedMessage("xray.app.proxyman.command.AddUserOperation", pbField(nil, 1, user))
	if _, err := a.call(handlerAlterInbound, alterInbound(tag, op)); err != nil {
		return fmt.Errorf("add %s to %s: %w", email, tag, err)
	}
	return nil
}

// removeUser removes a user from an inbound by email. A Hysteria2 inbound answers
// success whether it held the user or not.
func (a *xrayAPI) removeUser(tag, email string) error {
	op := typedMessage("xray.app.proxyman.command.RemoveUserOperation", pbField(nil, 1, []byte(email)))
	if _, err := a.call(handlerAlterInbound, alterInbound(tag, op)); err != nil {
		return fmt.Errorf("remove %s from %s: %w", email, tag, err)
	}
	return nil
}

// alterInbound encodes xray.app.proxyman.command.AlterInboundRequest.
func alterInbound(tag string, op []byte) []byte {
	return pbField(pbField(nil, 1, []byte(tag)), 2, op)
}

// typedMessage encodes xray.common.serial.TypedMessage: a message and its full name.
func typedMessage(name string, value []byte) []byte {
	return pbField(pbField(nil, 1, []byte(name)), 2, value)
}

// pbField appends a length-delimited protobuf field — every field these messages use is
// a string, bytes or a message.
func pbField(b []byte, field int, v []byte) []byte {
	b = binary.AppendUvarint(b, uint64(field)<<3|2)
	b = binary.AppendUvarint(b, uint64(len(v)))
	return append(b, v...)
}

// queryStats reads the counters whose names contain pattern, in one call and without
// asking again: the stats poll that uses it would rather be late than wait.
func (a *xrayAPI) queryStats(pattern string) (map[string]Traffic, error) {
	_, answer, err := a.callOnce(statsQueryStats, pbField(nil, 1, []byte(pattern))) // QueryStatsRequest.pattern
	if err != nil {
		return nil, err
	}
	return decodeQueryStats(answer)
}

// decodeQueryStats reads xray.app.stats.command.QueryStatsResponse: repeated Stat stat
// = 1, each a string name = 1 and an int64 value = 2.
func decodeQueryStats(msg []byte) (map[string]Traffic, error) {
	out := map[string]Traffic{}
	for len(msg) > 0 {
		field, stat, rest, err := pbNext(msg)
		if err != nil {
			return nil, err
		}
		msg = rest
		if field != 1 {
			continue
		}
		var name string
		var value int64
		for len(stat) > 0 {
			f, v, r, err := pbNext(stat)
			if err != nil {
				return nil, err
			}
			stat = r
			switch f {
			case 1:
				name = string(v)
			case 2:
				n, _ := binary.Uvarint(v)
				value = int64(n)
			}
		}
		foldStat(out, name, value)
	}
	return out, nil
}

// pbNext reads one protobuf field: its number and, for a length-delimited field, its
// bytes; for a varint, the varint's own bytes (for binary.Uvarint).
func pbNext(b []byte) (field int, value, rest []byte, err error) {
	key, n := binary.Uvarint(b)
	if n <= 0 {
		return 0, nil, nil, errors.New("protobuf: bad field key")
	}
	b = b[n:]
	switch key & 7 {
	case 0: // varint
		_, m := binary.Uvarint(b)
		if m <= 0 {
			return 0, nil, nil, errors.New("protobuf: bad varint")
		}
		return int(key >> 3), b[:m], b[m:], nil
	case 2: // length-delimited
		l, m := binary.Uvarint(b)
		if m <= 0 || uint64(len(b)-m) < l {
			return 0, nil, nil, errors.New("protobuf: bad length")
		}
		return int(key >> 3), b[m : m+int(l)], b[m+int(l):], nil
	default:
		return 0, nil, nil, fmt.Errorf("protobuf: unexpected wire type %d", key&7)
	}
}

// call makes one unary call, asked again while it cannot reach Xray the way runXrayAPI
// asks a CLI call again: a call that failed to connect sent nothing. It returns the
// answer's message.
func (a *xrayAPI) call(method string, msg []byte) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		sent, answer, err := a.callOnce(method, msg)
		if err == nil || sent || attempt >= len(apiDialRetries) {
			return answer, err
		}
		slog.Warn("xray: api call could not reach xray, asking again", "call", method, "attempt", attempt+1, "err", err)
		if a.wait != nil {
			a.wait(apiDialRetries[attempt])
		} else {
			time.Sleep(apiDialRetries[attempt])
		}
	}
}

// callOnce makes the call once and returns the answer's message. sent is false only
// when the connection could not be made, the one failure worth asking again.
func (a *xrayAPI) callOnce(method string, msg []byte) (sent bool, answer []byte, err error) {
	if len(msg) > apiMaxMessage {
		// Nothing was sent, but asking again would send the same message.
		return true, nil, fmt.Errorf("xray api: a %d-byte message is over the %d-byte limit", len(msg), apiMaxMessage)
	}
	frame := make([]byte, 5, 5+len(msg)) // uncompressed, then the length
	binary.BigEndian.PutUint32(frame[1:], uint32(len(msg)))
	frame = append(frame, msg...)

	ctx, cancel := context.WithTimeout(context.Background(), apiCallTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+a.addr+method, bytes.NewReader(frame))
	if err != nil {
		return false, nil, err
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("TE", "trailers")
	resp, err := a.client.Do(req)
	if err != nil {
		var op *net.OpError
		if errors.As(err, &op) && op.Op == "dial" {
			return false, nil, err
		}
		return true, nil, err
	}
	defer resp.Body.Close()
	// The status comes in the trailers, which are there once the body has been read.
	body, err := io.ReadAll(io.LimitReader(resp.Body, apiMaxAnswer))
	if err != nil {
		return true, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return true, nil, fmt.Errorf("http status %d", resp.StatusCode)
	}
	status, message := resp.Trailer.Get("Grpc-Status"), resp.Trailer.Get("Grpc-Message")
	if status == "" { // an error with no body comes back in the headers alone
		status, message = resp.Header.Get("Grpc-Status"), resp.Header.Get("Grpc-Message")
	}
	switch status {
	case "0":
		// One uncompressed frame: a flag byte, the length, the message.
		if len(body) >= 5 && body[0] == 0 && int(binary.BigEndian.Uint32(body[1:5])) == len(body)-5 {
			return true, body[5:], nil
		}
		if len(body) == 0 {
			return true, nil, nil
		}
		return true, nil, errors.New("the answer is not one uncompressed grpc frame")
	case "":
		return true, nil, errors.New("the answer carries no grpc-status")
	}
	if m, err := url.PathUnescape(message); err == nil {
		message = m
	}
	return true, nil, fmt.Errorf("grpc status %s: %s", status, message)
}
