//go:build linux

package xray

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"syscall"
)

// The kernel's sock_diag netlink family: what `ss` reads the socket table through, and
// what `ss -K` closes a socket with. Closing is SOCK_DESTROY on the socket's exact
// inet_diag id; the kernel then resets the connection and wakes whoever holds it — here
// Xray, which ends the proxied flow. It needs CAP_NET_ADMIN (the unit has it) and a
// kernel built with CONFIG_INET_DIAG_DESTROY (Ubuntu's is).
const (
	sockDiagByFamily = 20 // SOCK_DIAG_BY_FAMILY
	sockDestroy      = 21 // SOCK_DESTROY

	// inet_diag_sockid is 48 bytes: sport, dport (network order), src, dst (16 each,
	// network order), interface, cookie (two native u32).
	diagIDLen  = 48
	diagReqLen = 8 + diagIDLen // inet_diag_req_v2
	diagMsgLen = 4 + diagIDLen // the part of inet_diag_msg read here

	// tcpOpenStates are the TCP states a proxied connection can be cut in: everything but
	// LISTEN, TIME_WAIT and CLOSE, which hold no connection to end.
	tcpOpenStates = 1<<1 | 1<<2 | 1<<3 | 1<<4 | 1<<5 | 1<<8 | 1<<9 | 1<<11
)

// tcpSock is one TCP socket from the kernel's table: its two ends, and the id the kernel
// named it by, which SOCK_DESTROY needs back exactly (cookie included).
type tcpSock struct {
	family uint8
	local  netip.AddrPort
	remote netip.AddrPort
	id     [diagIDLen]byte
}

func openSockDiag() (int, error) {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, syscall.NETLINK_INET_DIAG)
	if err != nil {
		return -1, os.NewSyscallError("socket", err)
	}
	tv := syscall.Timeval{Sec: 5}
	if err := syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv); err != nil {
		syscall.Close(fd)
		return -1, os.NewSyscallError("setsockopt", err)
	}
	if err := syscall.Bind(fd, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}); err != nil {
		syscall.Close(fd)
		return -1, os.NewSyscallError("bind", err)
	}
	return fd, nil
}

// diagRequest builds one netlink message carrying an inet_diag_req_v2.
func diagRequest(typ uint16, flags uint16, seq uint32, family uint8, states uint32, id *[diagIDLen]byte) []byte {
	b := make([]byte, syscall.NLMSG_HDRLEN+diagReqLen)
	binary.NativeEndian.PutUint32(b[0:4], uint32(len(b)))
	binary.NativeEndian.PutUint16(b[4:6], typ)
	binary.NativeEndian.PutUint16(b[6:8], flags)
	binary.NativeEndian.PutUint32(b[8:12], seq)
	r := b[syscall.NLMSG_HDRLEN:]
	r[0] = family
	r[1] = syscall.IPPROTO_TCP
	binary.NativeEndian.PutUint32(r[4:8], states)
	if id != nil {
		copy(r[8:], id[:])
	}
	return b
}

func sendNetlink(fd int, msg []byte) error {
	for {
		err := syscall.Sendto(fd, msg, 0, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK})
		if err != syscall.EINTR {
			return os.NewSyscallError("sendto", err)
		}
	}
}

// recvNetlink reads one datagram. A socket with a receive timeout is not restarted
// after a signal (signal(7)), and the panel's own Xray CLI children send it SIGCHLD.
func recvNetlink(fd int, buf []byte) (int, error) {
	for {
		n, _, err := syscall.Recvfrom(fd, buf, 0)
		if err != syscall.EINTR {
			if err != nil {
				return 0, os.NewSyscallError("recvfrom", err)
			}
			return n, nil
		}
	}
}

// netlinkErrno is the errno an NLMSG_ERROR carries (0 is an ack).
func netlinkErrno(data []byte) syscall.Errno {
	if len(data) < 4 {
		return syscall.EINVAL
	}
	return syscall.Errno(-int32(binary.NativeEndian.Uint32(data[:4])))
}

// dumpTCP reads every open TCP socket, IPv4 and IPv6.
func dumpTCP() ([]tcpSock, error) {
	fd, err := openSockDiag()
	if err != nil {
		return nil, err
	}
	defer syscall.Close(fd)
	var out []tcpSock
	buf := make([]byte, 64*1024)
	for i, family := range []uint8{syscall.AF_INET, syscall.AF_INET6} {
		seq := uint32(i + 1)
		if err := sendNetlink(fd, diagRequest(sockDiagByFamily, syscall.NLM_F_REQUEST|syscall.NLM_F_DUMP, seq, family, tcpOpenStates, nil)); err != nil {
			return nil, err
		}
	read:
		for {
			n, err := recvNetlink(fd, buf)
			if err != nil {
				return nil, err
			}
			msgs, err := syscall.ParseNetlinkMessage(buf[:n])
			if err != nil {
				return nil, fmt.Errorf("sock_diag: %w", err)
			}
			for _, m := range msgs {
				if m.Header.Seq != seq {
					continue
				}
				switch m.Header.Type {
				case syscall.NLMSG_DONE:
					break read
				case syscall.NLMSG_ERROR:
					if e := netlinkErrno(m.Data); e != 0 {
						return nil, os.NewSyscallError("sock_diag dump", e)
					}
				default:
					if s, ok := parseDiagMsg(m.Data); ok {
						out = append(out, s)
					}
				}
			}
		}
	}
	return out, nil
}

// parseDiagMsg reads the family and id of an inet_diag_msg.
func parseDiagMsg(d []byte) (tcpSock, bool) {
	if len(d) < diagMsgLen {
		return tcpSock{}, false
	}
	s := tcpSock{family: d[0]}
	copy(s.id[:], d[4:4+diagIDLen])
	id := s.id[:]
	sport, dport := binary.BigEndian.Uint16(id[0:2]), binary.BigEndian.Uint16(id[2:4])
	var src, dst netip.Addr
	switch s.family {
	case syscall.AF_INET:
		src, dst = netip.AddrFrom4([4]byte(id[4:8])), netip.AddrFrom4([4]byte(id[20:24]))
	case syscall.AF_INET6:
		// An IPv4 client of a dual-stack listener shows up v4-mapped.
		src, dst = netip.AddrFrom16([16]byte(id[4:20])).Unmap(), netip.AddrFrom16([16]byte(id[20:36])).Unmap()
	default:
		return tcpSock{}, false
	}
	s.local, s.remote = netip.AddrPortFrom(src, sport), netip.AddrPortFrom(dst, dport)
	return s, true
}

// errSockGone is a socket that closed between the dump and the destroy.
var errSockGone = errors.New("socket already closed")

// destroyTCP closes the given sockets. It returns how many it closed, and the first
// error other than a socket having closed by itself in the meantime.
func destroyTCP(socks []tcpSock) (int, error) {
	if len(socks) == 0 {
		return 0, nil
	}
	fd, err := openSockDiag()
	if err != nil {
		return 0, err
	}
	defer syscall.Close(fd)
	buf := make([]byte, 4096)
	closed := 0
	var first error
	for i, s := range socks {
		seq := uint32(i + 1)
		if err := sendNetlink(fd, diagRequest(sockDestroy, syscall.NLM_F_REQUEST|syscall.NLM_F_ACK, seq, s.family, ^uint32(0), &s.id)); err != nil {
			return closed, err
		}
		err := readAck(fd, buf, seq)
		switch {
		case err == nil:
			closed++
		case errors.Is(err, errSockGone):
		case first == nil:
			first = err
		}
	}
	return closed, first
}

func readAck(fd int, buf []byte, seq uint32) error {
	for {
		n, err := recvNetlink(fd, buf)
		if err != nil {
			return err
		}
		msgs, err := syscall.ParseNetlinkMessage(buf[:n])
		if err != nil {
			return fmt.Errorf("sock_diag: %w", err)
		}
		for _, m := range msgs {
			if m.Header.Seq != seq || m.Header.Type != syscall.NLMSG_ERROR {
				continue
			}
			switch e := netlinkErrno(m.Data); e {
			case 0:
				return nil
			case syscall.ENOENT:
				return errSockGone
			default:
				return os.NewSyscallError("sock_diag destroy", e)
			}
		}
	}
}
